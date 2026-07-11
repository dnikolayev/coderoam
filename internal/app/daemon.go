package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/logging"
	"github.com/dnikolayev/coderoam/internal/router"
	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/types"
)

func (s *cliState) runCommand() *cobra.Command {
	var profile string
	var takeover bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the local WhatsApp bridge daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			if profile != "" {
				cfg.App.Profile = profile
			}
			if err := config.EnsureProfileDirs(cfg.App.Profile); err != nil {
				return err
			}
			releaseLock, err := acquireRunLock(cfg.App.Profile, takeover)
			if err != nil {
				return err
			}
			defer releaseLock()
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.EnsureProfile(ctx, cfg.App.Profile); err != nil {
				return err
			}
			recovered, err := store.RecoverAbandonedActiveInbox(ctx, cfg.App.Profile, "", activeInboxClaimStaleAfter)
			if err != nil {
				return err
			}
			if recovered > 0 {
				fmt.Printf("[active-inbox] recovered_claims=%d\n", recovered)
			}
			chatTransport, err := s.buildTransport(ctx, cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			bridgeRouter := router.New(cfg, store, chatTransport)
			defer bridgeRouter.Stop(context.Background())
			queueDepth := runQueueDepth(cfg)
			messages := make(chan types.IncomingMessage, queueDepth*runMaxInflight(cfg))
			groupEvents := make(chan types.GroupEvent, queueDepth)
			// liveCfg hands the config between the run loop's goroutines: the
			// group-events worker stores a fresh snapshot when a relay group is
			// archived, while the ticker and the post-connect startup path each
			// load once per pass. Sharing the local cfg variable directly would
			// tear concurrent reads of the large config.Config struct.
			liveCfg := newRunConfigHolder(cfg)
			configManager := newRunConfigManager(path, profile, liveCfg, bridgeRouter, store, chatTransport, func(format string, args ...any) {
				fmt.Printf(format, args...)
			})
			configAwareRouter := &runConfigRefreshingHandler{
				manager: configManager,
				next:    bridgeRouter,
				logf: func(format string, args ...any) {
					fmt.Printf(format, args...)
				},
			}
			var workers sync.WaitGroup
			workers.Add(1)
			go func() {
				defer workers.Done()
				dispatcher := newRunMessageDispatcher(ctx, configAwareRouter, cfg, func(format string, args ...any) {
					fmt.Printf(format, args...)
				})
				defer dispatcher.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case msg := <-messages:
						dispatcher.Dispatch(msg)
					}
				}
			}()
			workers.Add(1)
			go func() {
				defer workers.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case event := <-groupEvents:
						archived, err := configManager.HandleRelayGroupLifecycleEvent(ctx, event)
						if err != nil {
							fmt.Printf("[group-event] chat=%s error=%s\n", logging.Redact(event.ChatID), err)
							continue
						}
						if archived {
							fmt.Printf("[group-event] chat=%s archived=true\n", logging.Redact(event.ChatID))
						}
					}
				}
			}()
			workers.Add(1)
			go func() {
				defer workers.Done()
				ticker := time.NewTicker(2 * time.Second)
				defer ticker.Stop()
				for {
					tickCfg := liveCfg.Load()
					receipts, err := sendPendingActiveReadReceipts(ctx, store, chatTransport, tickCfg, 20)
					if err != nil {
						fmt.Printf("[active-read] error=%s\n", err)
					} else if receipts > 0 {
						fmt.Printf("[active-read] sent=%d\n", receipts)
					}
					sent, err := sendPendingActiveOutbox(ctx, store, chatTransport, tickCfg, 20)
					if err != nil {
						fmt.Printf("[active-outbox] error=%s\n", err)
					} else if sent > 0 {
						fmt.Printf("[active-outbox] sent=%d\n", sent)
					}
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
				}
			}()
			chatTransport.Subscribe(func(eventCtx context.Context, msg types.IncomingMessage) {
				select {
				case <-eventCtx.Done():
					return
				case <-ctx.Done():
					return
				case messages <- msg:
					fmt.Printf("[queued] chat=%s\n", logging.Redact(msg.ChatID))
				default:
					fmt.Printf("[ignored] chat=%s reason=message queue full\n", logging.Redact(msg.ChatID))
				}
			})
			chatTransport.SubscribeGroupEvents(func(eventCtx context.Context, event types.GroupEvent) {
				select {
				case <-eventCtx.Done():
					return
				case <-ctx.Done():
					return
				case groupEvents <- event:
					fmt.Printf("[group-event] chat=%s left=%d participants=%d deleted=%t\n", logging.Redact(event.ChatID), len(event.LeftParticipantIDs), event.ParticipantCount, event.Deleted)
				default:
					fmt.Printf("[ignored] chat=%s reason=group event queue full\n", logging.Redact(event.ChatID))
				}
			})
			if err := chatTransport.Connect(ctx); err != nil {
				return err
			}
			if _, err := configManager.Refresh(ctx); err != nil {
				fmt.Printf("[config] reload/reconcile error=%s; retaining current usable config\n", err)
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				runTransportWatchdog(ctx, chatTransport, 10*time.Second, func(format string, args ...any) {
					fmt.Printf(format, args...)
				})
			}()
			// Group events can arrive (and archive groups) as soon as the
			// transport is connected, so load one coherent snapshot for the
			// whole startup pass instead of reading the shared variable.
			cfg = liveCfg.Load()
			status, _ := chatTransport.Status(ctx)
			fmt.Printf("[connected] profile=%s account=%q %s\n", cfg.App.Profile, logging.Redact(status.Account), status.Detail)
			fmt.Printf("[watching] %d allowed groups\n", len(enabledGroups(cfg.Groups)))
			for _, group := range enabledGroups(cfg.Groups) {
				if group.Mode != config.GroupModeActiveSession {
					continue
				}
				migrated, err := store.MigrateMessagesToActiveInbox(ctx, cfg.App.Profile, group.ID, group.Alias, config.ActiveSessionID(group))
				if err != nil {
					return err
				}
				if migrated > 0 {
					fmt.Printf("[active-inbox] migrated=%d chat=%s\n", migrated, logging.Redact(group.ID))
				}
			}
			repaired, err := repairActiveInboxForConfig(ctx, store, cfg)
			if err != nil {
				return err
			}
			if repaired > 0 {
				fmt.Printf("[active-inbox] repaired_orphans=%d\n", repaired)
			}
			rescheduled, err := bridgeRouter.ScheduleUnreadActiveFallbacks(ctx, &cfg, 100)
			if err != nil {
				return err
			}
			if rescheduled > 0 {
				fmt.Printf("[active-inbox] fallback_rescheduled=%d\n", rescheduled)
			}
			pending, err := store.PendingIncomingMessages(ctx, cfg.App.Profile, queueDepth)
			if err != nil {
				return err
			}
			for _, msg := range pending {
				if group, ok := config.FindGroup(cfg, msg.ChatID); ok && group.Mode == config.GroupModeActiveSession {
					continue
				}
				select {
				case <-ctx.Done():
					return nil
				case messages <- msg:
					fmt.Printf("[queued] chat=%s reason=pending\n", logging.Redact(msg.ChatID))
				default:
					fmt.Printf("[ignored] chat=%s reason=pending queue full\n", logging.Redact(msg.ChatID))
				}
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				runConfigReloadLoop(ctx, configManager, time.Second, func(format string, args ...any) {
					fmt.Printf(format, args...)
				})
			}()
			fmt.Println("[ready] press Ctrl-C to stop")
			<-ctx.Done()
			fmt.Println("[shutdown] stopping bridge")
			workers.Wait()
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "profile name")
	cmd.Flags().BoolVar(&takeover, "takeover", false, "take over the messenger connection from an already-running daemon (stops the incumbent)")
	return cmd
}

// runConfigHolder shares the run daemon's live config between goroutines,
// mirroring the router's atomic.Pointer[config.Config] approach: Store
// publishes a new snapshot atomically and Load returns a private copy, so a
// concurrent swap can never tear a read of the large config.Config struct and
// callers cannot mutate the shared snapshot through reference-typed fields.
type runConfigHolder struct {
	ptr atomic.Pointer[config.Config]
}

func newRunConfigHolder(cfg config.Config) *runConfigHolder {
	holder := &runConfigHolder{}
	holder.Store(cfg)
	return holder
}

func (h *runConfigHolder) Load() config.Config {
	cfg := h.ptr.Load()
	if cfg == nil {
		return config.Config{}
	}
	return cloneRunConfig(*cfg)
}

func (h *runConfigHolder) Store(cfg config.Config) {
	snapshot := cloneRunConfig(cfg)
	h.ptr.Store(&snapshot)
}

func cloneRunConfig(cfg config.Config) config.Config {
	cfg.Groups = slices.Clone(cfg.Groups)
	cfg.Security.AdminSenderIDs = slices.Clone(cfg.Security.AdminSenderIDs)
	cfg.Security.AllowedSenderIDs = slices.Clone(cfg.Security.AllowedSenderIDs)
	if cfg.Runner != nil {
		runners := make(map[string]config.RunnerConfig, len(cfg.Runner))
		for id, runnerCfg := range cfg.Runner {
			runnerCfg.Args = slices.Clone(runnerCfg.Args)
			runnerCfg.Env = maps.Clone(runnerCfg.Env)
			runners[id] = runnerCfg
		}
		cfg.Runner = runners
	}
	return cfg
}

type runIncomingHandler interface {
	Handle(context.Context, types.IncomingMessage) router.ProcessResult
}

type runConfigTarget interface {
	SetConfigAndWait(context.Context, config.Config) error
	ScheduleUnreadActiveFallbacks(context.Context, *config.Config, int) (int, error)
}

type runConfigManager struct {
	mu                     sync.Mutex
	path                   string
	profileOverride        string
	holder                 *runConfigHolder
	target                 runConfigTarget
	store                  *db.Store
	transport              transport.ChatTransport
	logf                   func(string, ...any)
	reconcilePending       bool
	generationDrainPending bool
	pendingLifecycle       []types.GroupEvent
}

type runConfigRefreshingHandler struct {
	manager *runConfigManager
	next    runIncomingHandler
	logf    func(string, ...any)
}

func newRunConfigManager(path string, profileOverride string, holder *runConfigHolder, target runConfigTarget, store *db.Store, chatTransport transport.ChatTransport, logf func(string, ...any)) *runConfigManager {
	return &runConfigManager{
		path:            path,
		profileOverride: strings.TrimSpace(profileOverride),
		holder:          holder,
		target:          target,
		store:           store,
		transport:       chatTransport,
		logf:            logf,
	}
}

func (m *runConfigManager) Refresh(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed, err := m.refreshLocked(ctx)
	if err != nil {
		if retryErr := m.retryPendingLifecycleAgainstDiskLocked(ctx); retryErr != nil {
			return changed, errors.Join(err, retryErr)
		}
		return changed, err
	}
	if err := m.retryPendingLifecycleLocked(ctx); err != nil {
		return changed, err
	}
	return changed, nil
}

func (m *runConfigManager) refreshLocked(ctx context.Context) (bool, error) {
	updated, err := config.Load(m.path)
	if err != nil {
		return false, err
	}
	if m.profileOverride != "" {
		updated.App.Profile = m.profileOverride
	}
	current := m.holder.Load()
	changed := !reflect.DeepEqual(current, updated)
	if !changed && !m.reconcilePending && !m.generationDrainPending {
		return false, nil
	}
	if changed {
		if err := validateRunConfigReload(current, updated); err != nil {
			return false, err
		}
	}

	migrated := 0
	repaired := 0
	if changed {
		preMigrated, preRepaired, err := reconcileRunConfigBindings(ctx, m.store, updated)
		if err != nil {
			return false, err
		}
		migrated += preMigrated
		repaired += preRepaired
		m.holder.Store(updated)
		m.reconcilePending = true
		m.generationDrainPending = true
	}
	if m.generationDrainPending {
		if err := m.target.SetConfigAndWait(ctx, updated); err != nil {
			return changed, err
		}
		m.generationDrainPending = false
	}
	postMigrated, postRepaired, err := reconcileRunConfigBindings(ctx, m.store, updated)
	if err != nil {
		return changed, err
	}
	migrated += postMigrated
	repaired += postRepaired
	m.reconcilePending = true
	rescheduled, err := m.target.ScheduleUnreadActiveFallbacks(ctx, &updated, 100)
	if err != nil {
		return changed, err
	}
	m.reconcilePending = false
	if m.logf != nil {
		action := "reconciled"
		if changed {
			action = "reloaded"
		}
		m.logf("[config] %s groups=%d migrated=%d repaired=%d fallback_rescheduled=%d\n", action, len(enabledGroups(updated.Groups)), migrated, repaired, rescheduled)
	}
	return changed, nil
}

func reconcileRunConfigBindings(ctx context.Context, store *db.Store, cfg config.Config) (int, int, error) {
	migrated := 0
	for _, group := range enabledGroups(cfg.Groups) {
		if group.Mode != config.GroupModeActiveSession {
			continue
		}
		count, err := store.MigrateMessagesToActiveInbox(ctx, cfg.App.Profile, group.ID, group.Alias, config.ActiveSessionID(group))
		if err != nil {
			return 0, 0, err
		}
		migrated += count
	}
	repaired, err := repairActiveInboxForConfig(ctx, store, cfg)
	return migrated, repaired, err
}

func (m *runConfigManager) HandleRelayGroupLifecycleEvent(ctx context.Context, event types.GroupEvent) (bool, error) {
	if archive, _ := shouldArchiveRelayGroup(event); !archive {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.refreshLocked(ctx); err != nil {
		archived, diskErr := m.applyLifecycleEventToLatestDiskLocked(ctx, event)
		if diskErr == nil {
			return archived, nil
		}
		m.queueLifecycleEventLocked(event)
		return false, fmt.Errorf("refresh config before group lifecycle event: %w; disk lifecycle update failed and event was queued: %w", err, diskErr)
	}
	if err := m.retryPendingLifecycleLocked(ctx); err != nil {
		m.queueLifecycleEventLocked(event)
		return false, err
	}
	archived, err := m.applyLifecycleEventLocked(ctx, event)
	if err != nil {
		m.queueLifecycleEventLocked(event)
	}
	return archived, err
}

func (m *runConfigManager) applyLifecycleEventToLatestDiskLocked(ctx context.Context, event types.GroupEvent) (bool, error) {
	const maxConfigSaveAttempts = 5
	var updated config.Config
	var archived bool
	var err error
	for attempt := 1; attempt <= maxConfigSaveAttempts; attempt++ {
		diskCfg, loadErr := config.Load(m.path)
		if loadErr != nil {
			return false, loadErr
		}
		updated, archived, err = handleRelayGroupLifecycleEventForProfile(ctx, diskCfg, m.path, m.store, m.transport, event, m.holder.Load().App.Profile)
		if err == nil {
			if !archived && relayGroupArchived(diskCfg, event.ChatID) {
				updated = diskCfg
				archived = true
			}
			break
		}
		if !errors.Is(err, config.ErrConfigChanged) {
			return false, err
		}
	}
	if err != nil {
		return false, fmt.Errorf("archive relay group after %d concurrent config updates: %w", maxConfigSaveAttempts, err)
	}
	if !archived {
		return false, nil
	}
	liveCfg := m.holder.Load()
	for _, diskGroup := range updated.Groups {
		if diskGroup.ID != event.ChatID {
			continue
		}
		for i, liveGroup := range liveCfg.Groups {
			if liveGroup.ID != event.ChatID {
				continue
			}
			liveCfg.Groups = slices.Clone(liveCfg.Groups)
			liveCfg.Groups[i].Enabled = false
			liveCfg.Groups[i].Archived = true
			liveCfg.Groups[i].ArchivedAt = diskGroup.ArchivedAt
			liveCfg.Groups[i].ArchiveReason = diskGroup.ArchiveReason
			return true, m.publishLifecycleConfigLocked(ctx, liveCfg, event.ChatID)
		}
		break
	}
	return true, nil
}

func (m *runConfigManager) applyLifecycleEventLocked(ctx context.Context, event types.GroupEvent) (bool, error) {
	liveCfg := m.holder.Load()
	if relayGroupArchived(liveCfg, event.ChatID) {
		return true, m.publishLifecycleConfigLocked(ctx, liveCfg, event.ChatID)
	}
	updated, archived, err := handleRelayGroupLifecycleEvent(ctx, liveCfg, m.path, m.store, m.transport, event)
	if err != nil || !archived {
		return archived, err
	}
	return true, m.publishLifecycleConfigLocked(ctx, updated, event.ChatID)
}

func (m *runConfigManager) publishLifecycleConfigLocked(ctx context.Context, cfg config.Config, chatID string) error {
	m.holder.Store(cfg)
	m.generationDrainPending = true
	if err := m.target.SetConfigAndWait(ctx, cfg); err != nil {
		return err
	}
	m.generationDrainPending = false
	// A previous-generation handler may have written after the lifecycle
	// helper's first cleanup but before the config generation drained.
	_, err := m.store.DeleteChatData(ctx, cfg.App.Profile, chatID)
	return err
}

func (m *runConfigManager) queueLifecycleEventLocked(event types.GroupEvent) {
	for _, pending := range m.pendingLifecycle {
		if pending.ChatID == event.ChatID {
			return
		}
	}
	m.pendingLifecycle = append(m.pendingLifecycle, event)
}

func (m *runConfigManager) retryPendingLifecycleLocked(ctx context.Context) error {
	for len(m.pendingLifecycle) > 0 {
		event := m.pendingLifecycle[0]
		archived, err := m.applyLifecycleEventLocked(ctx, event)
		if err != nil {
			return fmt.Errorf("retry queued group lifecycle event: %w", err)
		}
		m.pendingLifecycle = m.pendingLifecycle[1:]
		if archived && m.logf != nil {
			m.logf("[group-event] chat=%s archived=true reason=queued-retry\n", logging.Redact(event.ChatID))
		}
	}
	return nil
}

func (m *runConfigManager) retryPendingLifecycleAgainstDiskLocked(ctx context.Context) error {
	for len(m.pendingLifecycle) > 0 {
		event := m.pendingLifecycle[0]
		archived, err := m.applyLifecycleEventToLatestDiskLocked(ctx, event)
		if err != nil {
			return fmt.Errorf("retry queued group lifecycle event against disk config: %w", err)
		}
		m.pendingLifecycle = m.pendingLifecycle[1:]
		if archived && m.logf != nil {
			m.logf("[group-event] chat=%s archived=true reason=queued-disk-retry\n", logging.Redact(event.ChatID))
		}
	}
	return nil
}

func relayGroupArchived(cfg config.Config, chatID string) bool {
	for _, group := range cfg.Groups {
		if group.ID == chatID && group.Mode == config.GroupModeActiveSession && group.RelayManaged && group.Archived && !group.Enabled {
			return true
		}
	}
	return false
}

func validateRunConfigReload(current config.Config, updated config.Config) error {
	if current.App != updated.App {
		return fmt.Errorf("app settings changed; restart coderoam to apply them")
	}
	if current.Transport != updated.Transport {
		return fmt.Errorf("transport settings changed; restart coderoam to apply them")
	}
	if current.Concurrency != updated.Concurrency || current.RateLimits.MaxParallelGroups != updated.RateLimits.MaxParallelGroups {
		return fmt.Errorf("daemon concurrency settings changed; restart coderoam to apply them")
	}
	return nil
}

func (h *runConfigRefreshingHandler) Handle(ctx context.Context, msg types.IncomingMessage) router.ProcessResult {
	if _, err := h.manager.Refresh(ctx); err != nil && h.logf != nil {
		h.logf("[config] reload/reconcile error=%s; retaining current usable config\n", err)
	}
	return h.next.Handle(ctx, msg)
}

func runConfigReloadLoop(ctx context.Context, manager *runConfigManager, interval time.Duration, logf func(string, ...any)) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastError := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := manager.Refresh(ctx)
			if err == nil {
				if lastError != "" && logf != nil {
					logf("[config] reload recovered\n")
				}
				lastError = ""
				continue
			}
			if err.Error() != lastError && logf != nil {
				logf("[config] reload/reconcile error=%s; retaining current usable config\n", err)
			}
			lastError = err.Error()
		}
	}
}

type runMessageDispatcher struct {
	ctx         context.Context
	cancel      context.CancelFunc
	handler     runIncomingHandler
	logf        func(string, ...any)
	queueDepth  int
	queuePolicy string
	globalSlots chan struct{}

	mu     sync.Mutex
	groups map[string]chan types.IncomingMessage
	wg     sync.WaitGroup
}

func newRunMessageDispatcher(ctx context.Context, handler runIncomingHandler, cfg config.Config, logf func(string, ...any)) *runMessageDispatcher {
	dispatchCtx, cancel := context.WithCancel(ctx)
	return &runMessageDispatcher{
		ctx:         dispatchCtx,
		cancel:      cancel,
		handler:     handler,
		logf:        logf,
		queueDepth:  runQueueDepth(cfg),
		queuePolicy: cfg.Concurrency.QueueOverflowPolicy,
		globalSlots: make(chan struct{}, runMaxInflight(cfg)),
		groups:      map[string]chan types.IncomingMessage{},
	}
}

func runQueueDepth(cfg config.Config) int {
	if cfg.Concurrency.QueueMaxDepthPerGroup > 0 {
		return cfg.Concurrency.QueueMaxDepthPerGroup
	}
	return 50
}

func runMaxInflight(cfg config.Config) int {
	if cfg.Concurrency.GlobalMaxInflight > 0 {
		return cfg.Concurrency.GlobalMaxInflight
	}
	if cfg.RateLimits.MaxParallelGroups > 0 {
		return cfg.RateLimits.MaxParallelGroups
	}
	return 5
}

func runTransportWatchdog(ctx context.Context, chatTransport transport.ChatTransport, interval time.Duration, logf func(string, ...any)) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconnected, err := ensureRunTransportConnected(ctx, chatTransport)
			if err != nil {
				if logf != nil {
					logf("[transport] reconnect error=%s\n", err)
				}
				continue
			}
			if reconnected && logf != nil {
				status, _ := chatTransport.Status(ctx)
				account := ""
				detail := ""
				if status != nil {
					account = status.Account
					detail = status.Detail
				}
				logf("[transport] reconnected account=%q %s\n", logging.Redact(account), detail)
			}
		}
	}
}

func ensureRunTransportConnected(ctx context.Context, chatTransport transport.ChatTransport) (bool, error) {
	if chatTransport == nil {
		return false, fmt.Errorf("transport is not configured")
	}
	status, statusErr := chatTransport.Status(ctx)
	if statusErr == nil && status != nil && status.Connected {
		return false, nil
	}
	if err := chatTransport.Connect(ctx); err != nil {
		if statusErr != nil {
			return false, fmt.Errorf("status: %w; reconnect: %w", statusErr, err)
		}
		return false, err
	}
	return true, nil
}

func (d *runMessageDispatcher) Dispatch(msg types.IncomingMessage) bool {
	select {
	case <-d.ctx.Done():
		return false
	default:
	}
	queue := d.groupQueue(msg.ChatID)
	select {
	case <-d.ctx.Done():
		return false
	case queue <- msg:
		return true
	default:
		return d.handleFullQueue(queue, msg)
	}
}

func (d *runMessageDispatcher) handleFullQueue(queue chan types.IncomingMessage, msg types.IncomingMessage) bool {
	if d.queuePolicy != "drop_oldest_with_notice" {
		d.logResult(msg, router.ProcessResult{Ignored: true, Reason: "message queue full"})
		return false
	}
	select {
	case <-d.ctx.Done():
		return false
	case dropped := <-queue:
		d.logResult(dropped, router.ProcessResult{Ignored: true, Reason: "message queue overflow dropped oldest"})
	default:
		d.logResult(msg, router.ProcessResult{Ignored: true, Reason: "message queue full"})
		return false
	}
	select {
	case <-d.ctx.Done():
		return false
	case queue <- msg:
		return true
	default:
		d.logResult(msg, router.ProcessResult{Ignored: true, Reason: "message queue full"})
		return false
	}
}

func (d *runMessageDispatcher) Stop() {
	d.cancel()
	d.wg.Wait()
}

func (d *runMessageDispatcher) groupQueue(chatID string) chan types.IncomingMessage {
	if chatID == "" {
		chatID = "<empty-chat-id>"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if queue, ok := d.groups[chatID]; ok {
		return queue
	}
	queue := make(chan types.IncomingMessage, d.queueDepth)
	d.groups[chatID] = queue
	d.wg.Add(1)
	go d.runGroup(queue)
	return queue
}

func (d *runMessageDispatcher) runGroup(queue <-chan types.IncomingMessage) {
	defer d.wg.Done()
	for {
		select {
		case <-d.ctx.Done():
			return
		case msg := <-queue:
			if !d.acquireSlot() {
				return
			}
			result := d.handler.Handle(d.ctx, msg)
			d.releaseSlot()
			d.logResult(msg, result)
		}
	}
}

func (d *runMessageDispatcher) acquireSlot() bool {
	select {
	case <-d.ctx.Done():
		return false
	case d.globalSlots <- struct{}{}:
		return true
	}
}

func (d *runMessageDispatcher) releaseSlot() {
	select {
	case <-d.globalSlots:
	default:
	}
}

func (d *runMessageDispatcher) logResult(msg types.IncomingMessage, result router.ProcessResult) {
	if d.logf == nil {
		return
	}
	if result.Ignored {
		d.logf("[ignored] chat=%s reason=%s\n", logging.Redact(msg.ChatID), result.Reason)
		return
	}
	d.logf("[processed] chat=%s replies=%d\n", logging.Redact(msg.ChatID), len(result.Sent))
}

func sendPendingActiveOutbox(ctx context.Context, store *db.Store, chatTransport transport.ChatTransport, cfg config.Config, limit int) (int, error) {
	records, err := store.PendingActiveOutbox(ctx, cfg.App.Profile, limit)
	if err != nil {
		return 0, err
	}
	return sendActiveOutboxBatches(ctx, store, chatTransport, cfg, activeOutboxBatches(records))
}

func sendActiveOutboxBatches(ctx context.Context, store *db.Store, chatTransport transport.ChatTransport, cfg config.Config, batches [][]db.ActiveOutboxRecord) (int, error) {
	if len(batches) == 0 {
		return 0, nil
	}
	slots := make(chan struct{}, runMaxInflight(cfg))
	var wg sync.WaitGroup
	var mu sync.Mutex
	sent := 0
	var firstErr error
	var launchErr error
launch:
	for _, batch := range batches {
		select {
		case <-ctx.Done():
			launchErr = ctx.Err()
			break launch
		case slots <- struct{}{}:
		}
		batch := batch
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			batchSent, err := sendActiveOutboxBatch(ctx, store, chatTransport, cfg, batch)
			mu.Lock()
			sent += batchSent
			if err != nil && firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return sent, firstErr
	}
	return sent, launchErr
}

func sendActiveOutboxBatch(ctx context.Context, store *db.Store, chatTransport transport.ChatTransport, cfg config.Config, records []db.ActiveOutboxRecord) (int, error) {
	sent := 0
	for _, record := range records {
		if _, err := chatTransport.SendText(ctx, record.ChatID, record.Text, types.SendOptions{TypingIndicator: cfg.Reply.TypingIndicator}); err != nil {
			if markErr := store.MarkActiveOutboxFailed(ctx, record.ID, err); markErr != nil {
				return sent, markErr
			}
			continue
		}
		if err := store.MarkActiveOutboxSent(ctx, record.ID); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

func sendPendingActiveReadReceipts(ctx context.Context, store *db.Store, chatTransport transport.ChatTransport, cfg config.Config, limit int) (int, error) {
	records, err := store.PendingActiveReadReceipts(ctx, cfg.App.Profile, limit)
	if err != nil {
		return 0, err
	}
	return sendActiveReadReceiptBatches(ctx, store, chatTransport, cfg, activeReadReceiptBatches(records))
}

func sendActiveReadReceiptBatches(ctx context.Context, store *db.Store, chatTransport transport.ChatTransport, cfg config.Config, batches [][]db.ActiveReadReceiptRecord) (int, error) {
	if len(batches) == 0 {
		return 0, nil
	}
	slots := make(chan struct{}, runMaxInflight(cfg))
	var wg sync.WaitGroup
	var mu sync.Mutex
	sent := 0
	var firstErr error
	var launchErr error
launch:
	for _, batch := range batches {
		select {
		case <-ctx.Done():
			launchErr = ctx.Err()
			break launch
		case slots <- struct{}{}:
		}
		batch := batch
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			batchSent, err := sendActiveReadReceiptBatch(ctx, store, chatTransport, batch)
			mu.Lock()
			sent += batchSent
			if err != nil && firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return sent, firstErr
	}
	return sent, launchErr
}

func sendActiveReadReceiptBatch(ctx context.Context, store *db.Store, chatTransport transport.ChatTransport, records []db.ActiveReadReceiptRecord) (int, error) {
	sent := 0
	for _, record := range records {
		msg := types.IncomingMessage{
			ID:       record.ExternalMessageID,
			ChatID:   record.ChatID,
			SenderID: record.SenderID,
		}
		if err := chatTransport.MarkRead(ctx, msg); err != nil {
			if markErr := store.MarkActiveReadReceiptFailed(ctx, record.ID, err); markErr != nil {
				return sent, markErr
			}
			continue
		}
		if err := store.MarkActiveReadReceiptSent(ctx, record.ID); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

func activeOutboxBatches(records []db.ActiveOutboxRecord) [][]db.ActiveOutboxRecord {
	positions := map[string]int{}
	batches := [][]db.ActiveOutboxRecord{}
	for _, record := range records {
		index, ok := positions[record.ChatID]
		if !ok {
			index = len(batches)
			positions[record.ChatID] = index
			batches = append(batches, nil)
		}
		batches[index] = append(batches[index], record)
	}
	return batches
}

func activeReadReceiptBatches(records []db.ActiveReadReceiptRecord) [][]db.ActiveReadReceiptRecord {
	positions := map[string]int{}
	batches := [][]db.ActiveReadReceiptRecord{}
	for _, record := range records {
		index, ok := positions[record.ChatID]
		if !ok {
			index = len(batches)
			positions[record.ChatID] = index
			batches = append(batches, nil)
		}
		batches[index] = append(batches[index], record)
	}
	return batches
}

func handleRelayGroupLifecycleEvent(ctx context.Context, cfg config.Config, configPath string, store *db.Store, chatTransport transport.ChatTransport, event types.GroupEvent) (config.Config, bool, error) {
	return handleRelayGroupLifecycleEventForProfile(ctx, cfg, configPath, store, chatTransport, event, cfg.App.Profile)
}

func handleRelayGroupLifecycleEventForProfile(ctx context.Context, cfg config.Config, configPath string, store *db.Store, chatTransport transport.ChatTransport, event types.GroupEvent, profileID string) (config.Config, bool, error) {
	groupIndex := -1
	for i, group := range cfg.Groups {
		if group.ID == event.ChatID && group.Mode == config.GroupModeActiveSession && group.RelayManaged && group.Enabled && !group.Archived {
			groupIndex = i
			break
		}
	}
	if groupIndex < 0 {
		return cfg, false, nil
	}
	archive, reason := shouldArchiveRelayGroup(event)
	if !archive {
		return cfg, false, nil
	}
	group := cfg.Groups[groupIndex]
	archiveErrText := ""
	if chatTransport != nil {
		if err := chatTransport.ArchiveChat(ctx, group.ID); err != nil {
			archiveErrText = err.Error()
		}
	}
	deletedRows, err := store.DeleteChatData(ctx, profileID, group.ID)
	if err != nil {
		return cfg, false, err
	}
	group.Enabled = false
	group.Archived = true
	group.ArchivedAt = time.Now().UTC().Format(time.RFC3339Nano)
	group.ArchiveReason = reason
	// Replace, never mutate: cfg.Groups shares its backing array with the
	// caller's snapshot (and any other goroutine still reading it), so write
	// the archived entry into a fresh clone instead of the shared array.
	cfg.Groups = slices.Clone(cfg.Groups)
	cfg.Groups[groupIndex] = group
	if err := config.Save(configPath, cfg); err != nil {
		return cfg, false, err
	}
	_ = store.Audit(ctx, profileID, "relay_group_archived", event.SenderID, group.ID, map[string]any{
		"alias":                group.Alias,
		"session_id":           config.ActiveSessionID(group),
		"reason":               reason,
		"left_participant_ids": event.LeftParticipantIDs,
		"participant_count":    event.ParticipantCount,
		"deleted":              event.Deleted,
		"deleted_local_rows":   deletedRows,
		"device_archive_error": archiveErrText,
		"reactivation_command": fmt.Sprintf("coderoam active start --name %q --alias %s --session-id %s --participants <owner> --yes", group.Alias, group.Alias, config.ActiveSessionID(group)),
	})
	return cfg, true, nil
}

func shouldArchiveRelayGroup(event types.GroupEvent) (bool, string) {
	if event.Deleted {
		return true, "group deleted"
	}
	if len(event.LeftParticipantIDs) > 0 {
		return true, "participant left"
	}
	if event.ParticipantCount == 1 {
		return true, "no human participants remain"
	}
	return false, ""
}
