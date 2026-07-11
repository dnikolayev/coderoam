package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/router"
	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/types"
)

// TestHandleRelayGroupLifecycleEventDoesNotMutateCallerConfig pins the
// replace-not-mutate contract: archiving a relay group must build a fresh
// Groups slice rather than writing through the backing array shared with the
// caller's snapshot. Before the fix this test failed because the shared entry
// was flipped to Archived in place.
func TestHandleRelayGroupLifecycleEventDoesNotMutateCallerConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "daemon-test.sqlite3"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, "bot"); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	original := config.Config{}
	original.App.Profile = "bot"
	original.Groups = []config.GroupConfig{{
		ID:              "123@g.us",
		Alias:           "relay",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "relay-session",
		Enabled:         true,
		RelayManaged:    true,
	}}
	// shared aliases the same backing array as original.Groups, standing in
	// for every other goroutine still reading the pre-event snapshot.
	shared := original.Groups

	event := types.GroupEvent{
		ChatID:             "123@g.us",
		SenderID:           "owner@s.whatsapp.net",
		LeftParticipantIDs: []string{"owner@s.whatsapp.net"},
		ParticipantCount:   2,
		Timestamp:          time.Now(),
	}
	updated, archived, err := handleRelayGroupLifecycleEvent(ctx, original, filepath.Join(dir, "config.toml"), store, nil, event)
	if err != nil {
		t.Fatalf("handleRelayGroupLifecycleEvent: %v", err)
	}
	if !archived {
		t.Fatal("expected the participant-left event to archive the relay group")
	}
	if !updated.Groups[0].Archived || updated.Groups[0].Enabled {
		t.Fatalf("updated config should carry the archived group, got %+v", updated.Groups[0])
	}
	if shared[0].Archived || !shared[0].Enabled || shared[0].ArchivedAt != "" {
		t.Fatalf("handleRelayGroupLifecycleEvent mutated the shared Groups backing array: %+v", shared[0])
	}
}

// TestRunConfigHolderConcurrentLoadStore hammers the holder from a writer and
// several readers under -race and checks each loaded snapshot is internally
// consistent (profile and groups always belong to the same stored config).
// The holder is new with the fix, so this pins post-fix semantics; the
// pre-fix code shared a bare local variable with no equivalent to exercise.
func TestRunConfigHolderConcurrentLoadStore(t *testing.T) {
	t.Parallel()
	configA := config.Config{}
	configA.App.Profile = "profile-a"
	configA.Groups = []config.GroupConfig{{ID: "chat-a@g.us", Enabled: true}}

	configB := config.Config{}
	configB.App.Profile = "profile-b"
	configB.Groups = []config.GroupConfig{
		{ID: "chat-b@g.us", Enabled: true},
		{ID: "chat-c@g.us", Enabled: true, Archived: true},
	}

	holder := newRunConfigHolder(configA)

	done := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			if i%2 == 0 {
				holder.Store(configB)
			} else {
				holder.Store(configA)
			}
		}
	}()

	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 5000; i++ {
				cfg := holder.Load()
				switch cfg.App.Profile {
				case "profile-a":
					if len(cfg.Groups) != 1 || cfg.Groups[0].ID != "chat-a@g.us" {
						t.Errorf("torn read: profile-a paired with groups %+v", cfg.Groups)
						return
					}
				case "profile-b":
					if len(cfg.Groups) != 2 || cfg.Groups[1].ID != "chat-c@g.us" {
						t.Errorf("torn read: profile-b paired with groups %+v", cfg.Groups)
						return
					}
				default:
					t.Errorf("torn read: unexpected profile %q", cfg.App.Profile)
					return
				}
			}
		}()
	}
	readers.Wait()
	close(done)
	writer.Wait()
}

func TestRunConfigHolderDeepClonesReferenceFields(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	cfg.App.Profile = "bot"
	cfg.Security.AdminSenderIDs = []string{"admin@lid"}
	cfg.Security.AllowedSenderIDs = []string{"allowed@lid"}
	cfg.Groups = []config.GroupConfig{{
		ID:              "chat@g.us",
		Alias:           "codex-session",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "codex-session",
		Enabled:         true,
	}}
	cfg.Runner = map[string]config.RunnerConfig{
		"codex-code": {
			Mode:    "process-once-json",
			Command: "/bin/codex-runner",
			Args:    []string{"--prompt"},
			Env:     map[string]string{"SESSION": "codex-session"},
		},
	}

	holder := newRunConfigHolder(cfg)

	cfg.Groups[0].ID = "mutated-chat@g.us"
	cfg.Security.AdminSenderIDs[0] = "mutated-admin@lid"
	cfg.Security.AllowedSenderIDs[0] = "mutated-allowed@lid"
	runnerCfg := cfg.Runner["codex-code"]
	runnerCfg.Args[0] = "--mutated"
	runnerCfg.Env["SESSION"] = "mutated-session"
	cfg.Runner["codex-code"] = runnerCfg

	loaded := holder.Load()
	if loaded.Groups[0].ID != "chat@g.us" {
		t.Fatalf("Store did not clone groups: %+v", loaded.Groups)
	}
	if loaded.Security.AdminSenderIDs[0] != "admin@lid" || loaded.Security.AllowedSenderIDs[0] != "allowed@lid" {
		t.Fatalf("Store did not clone sender allowlists: admin=%+v allowed=%+v", loaded.Security.AdminSenderIDs, loaded.Security.AllowedSenderIDs)
	}
	loadedRunner := loaded.Runner["codex-code"]
	if loadedRunner.Args[0] != "--prompt" || loadedRunner.Env["SESSION"] != "codex-session" {
		t.Fatalf("Store did not clone runner config: %+v", loadedRunner)
	}

	loaded.Groups[0].ID = "loaded-mutated-chat@g.us"
	loaded.Security.AdminSenderIDs[0] = "loaded-mutated-admin@lid"
	loaded.Security.AllowedSenderIDs[0] = "loaded-mutated-allowed@lid"
	loadedRunner.Args[0] = "--loaded-mutated"
	loadedRunner.Env["SESSION"] = "loaded-mutated-session"
	loaded.Runner["codex-code"] = loadedRunner

	reloaded := holder.Load()
	reloadedRunner := reloaded.Runner["codex-code"]
	if reloaded.Groups[0].ID != "chat@g.us" ||
		reloaded.Security.AdminSenderIDs[0] != "admin@lid" ||
		reloaded.Security.AllowedSenderIDs[0] != "allowed@lid" ||
		reloadedRunner.Args[0] != "--prompt" ||
		reloadedRunner.Env["SESSION"] != "codex-session" {
		t.Fatalf("Load exposed mutable snapshot state: cfg=%+v runner=%+v", reloaded, reloadedRunner)
	}
}

func TestRunConfigHolderZeroValueLoad(t *testing.T) {
	t.Parallel()
	var holder runConfigHolder
	got := holder.Load()
	if got.App.Profile != "" || len(got.Groups) != 0 || len(got.Runner) != 0 || len(got.Security.AdminSenderIDs) != 0 || len(got.Security.AllowedSenderIDs) != 0 {
		t.Fatalf("zero-value holder load = %+v", got)
	}
}

func TestRunConfigRefreshingHandlerAcceptsGroupAddedAfterDaemonStart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	initial.Security.RequireGroupAllowlist = true
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}

	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	bridgeRouter := router.New(initial, store, nil)
	defer bridgeRouter.Stop(context.Background())
	holder := newRunConfigHolder(initial)
	manager := newRunConfigManager(path, "", holder, bridgeRouter, store, nil, nil)
	handler := &runConfigRefreshingHandler{manager: manager, next: bridgeRouter}

	updated := initial
	updated.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-1",
		Enabled:         true,
		RelayManaged:    true,
	}}
	if err := config.Save(path, updated); err != nil {
		t.Fatal(err)
	}

	result := handler.Handle(ctx, types.IncomingMessage{
		ID:        "new-group-message",
		ChatID:    "mrf-1@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "owner@lid",
		Text:      "status?",
		Timestamp: time.Now(),
	})
	if result.Ignored {
		t.Fatalf("message from newly configured group was ignored: %+v", result)
	}
	records, err := store.ListActiveInbox(ctx, initial.App.Profile, "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].SessionID != "mrf-1" || records[0].ExternalMessageID != "new-group-message" {
		t.Fatalf("active inbox records = %+v", records)
	}
	if loaded := holder.Load(); len(loaded.Groups) != 1 || loaded.Groups[0].Alias != "mrf-1" {
		t.Fatalf("live config was not reloaded: %+v", loaded.Groups)
	}
}

func TestRunConfigRefreshingHandlerKeepsLastKnownGoodConfig(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	initial.Security.RequireGroupAllowlist = true
	initial.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-1",
		Enabled:         true,
	}}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}

	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	bridgeRouter := router.New(initial, store, nil)
	defer bridgeRouter.Stop(context.Background())
	holder := newRunConfigHolder(initial)
	manager := newRunConfigManager(path, "", holder, bridgeRouter, store, nil, nil)
	var logs strings.Builder
	handler := &runConfigRefreshingHandler{
		manager: manager,
		next:    bridgeRouter,
		logf: func(format string, args ...any) {
			fmt.Fprintf(&logs, format, args...)
		},
	}
	if err := os.WriteFile(path, []byte("[[groups]\ninvalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := handler.Handle(ctx, types.IncomingMessage{
		ID:        "last-known-good-message",
		ChatID:    "mrf-1@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "owner@lid",
		Text:      "status?",
		Timestamp: time.Now(),
	})
	if result.Ignored {
		t.Fatalf("last-known-good group was ignored after reload error: %+v", result)
	}
	if !strings.Contains(logs.String(), "retaining current usable config") {
		t.Fatalf("reload error log = %q", logs.String())
	}
	if loaded := holder.Load(); len(loaded.Groups) != 1 || loaded.Groups[0].Alias != "mrf-1" {
		t.Fatalf("invalid reload replaced live config: %+v", loaded.Groups)
	}
}

func TestRunConfigManagerLifecyclePreservesDiskReload(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	initial.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-1",
		Enabled:         true,
		RelayManaged:    true,
	}}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	bridgeRouter := router.New(initial, store, nil)
	defer bridgeRouter.Stop(context.Background())
	holder := newRunConfigHolder(initial)
	manager := newRunConfigManager(path, "", holder, bridgeRouter, store, nil, nil)

	updated := initial
	updated.Groups = append(updated.Groups, config.GroupConfig{
		ID:              "mrf-5@g.us",
		Alias:           "mrf-5",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-5",
		Enabled:         true,
		RelayManaged:    true,
	})
	if err := config.Save(path, updated); err != nil {
		t.Fatal(err)
	}
	archived, err := manager.HandleRelayGroupLifecycleEvent(ctx, types.GroupEvent{
		ChatID:             "mrf-1@g.us",
		SenderID:           "owner@lid",
		LeftParticipantIDs: []string{"owner@lid"},
		ParticipantCount:   2,
		Timestamp:          time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !archived {
		t.Fatal("expected mrf-1 to be archived")
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Groups) != 2 {
		t.Fatalf("groups after reload plus lifecycle event = %+v", loaded.Groups)
	}
	if !loaded.Groups[0].Archived || loaded.Groups[0].Enabled {
		t.Fatalf("mrf-1 was not archived: %+v", loaded.Groups[0])
	}
	if loaded.Groups[1].Alias != "mrf-5" || !loaded.Groups[1].Enabled || loaded.Groups[1].Archived {
		t.Fatalf("disk-reloaded mrf-5 group was lost: %+v", loaded.Groups[1])
	}
}

func TestRunConfigManagerRetriesLifecycleEventAfterInvalidConfig(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	initial.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-1",
		Enabled:         true,
		RelayManaged:    true,
	}}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	bridgeRouter := router.New(initial, store, nil)
	defer bridgeRouter.Stop(context.Background())
	holder := newRunConfigHolder(initial)
	manager := newRunConfigManager(path, "", holder, bridgeRouter, store, nil, nil)
	if err := os.WriteFile(path, []byte("[[groups]\ninvalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	archived, err := manager.HandleRelayGroupLifecycleEvent(ctx, types.GroupEvent{
		ChatID:             "mrf-1@g.us",
		SenderID:           "owner@lid",
		LeftParticipantIDs: []string{"owner@lid"},
		ParticipantCount:   2,
		Timestamp:          time.Now(),
	})
	if err == nil || archived {
		t.Fatalf("invalid config lifecycle result archived=%t err=%v", archived, err)
	}
	if len(manager.pendingLifecycle) != 1 {
		t.Fatalf("pending lifecycle events = %+v", manager.pendingLifecycle)
	}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(manager.pendingLifecycle) != 0 {
		t.Fatalf("pending lifecycle event was not drained: %+v", manager.pendingLifecycle)
	}
	if len(loaded.Groups) != 1 || !loaded.Groups[0].Archived || loaded.Groups[0].Enabled {
		t.Fatalf("queued lifecycle event was not applied: %+v", loaded.Groups)
	}
}

func TestRunConfigManagerDoesNotLetBenignEventHideQueuedArchive(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	initial.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-1",
		Enabled:         true,
		RelayManaged:    true,
	}}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	bridgeRouter := router.New(initial, store, nil)
	defer bridgeRouter.Stop(context.Background())
	holder := newRunConfigHolder(initial)
	manager := newRunConfigManager(path, "", holder, bridgeRouter, store, nil, nil)
	if err := os.WriteFile(path, []byte("[[groups]\ninvalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	archived, err := manager.HandleRelayGroupLifecycleEvent(ctx, types.GroupEvent{
		ChatID:           "mrf-1@g.us",
		ParticipantCount: 3,
		Timestamp:        time.Now(),
	})
	if err != nil || archived || len(manager.pendingLifecycle) != 0 {
		t.Fatalf("benign event archived=%t err=%v pending=%+v", archived, err, manager.pendingLifecycle)
	}
	archived, err = manager.HandleRelayGroupLifecycleEvent(ctx, types.GroupEvent{
		ChatID:    "mrf-1@g.us",
		Deleted:   true,
		Timestamp: time.Now(),
	})
	if err == nil || archived || len(manager.pendingLifecycle) != 1 || !manager.pendingLifecycle[0].Deleted {
		t.Fatalf("delete event archived=%t err=%v pending=%+v", archived, err, manager.pendingLifecycle)
	}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Groups) != 1 || !loaded.Groups[0].Archived {
		t.Fatalf("queued delete was not applied: %+v", loaded.Groups)
	}
}

func TestRunConfigManagerArchivesThroughRestartOnlyDiskChange(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	initial.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-1",
		Enabled:         true,
		RelayManaged:    true,
	}}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	bridgeRouter := router.New(initial, store, nil)
	defer bridgeRouter.Stop(context.Background())
	holder := newRunConfigHolder(initial)
	manager := newRunConfigManager(path, "", holder, bridgeRouter, store, nil, nil)
	restartOnly, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	restartOnly.Transport.DownloadMedia = true
	if err := config.Save(path, restartOnly); err != nil {
		t.Fatal(err)
	}

	archived, err := manager.HandleRelayGroupLifecycleEvent(ctx, types.GroupEvent{
		ChatID:    "mrf-1@g.us",
		Deleted:   true,
		Timestamp: time.Now(),
	})
	if err != nil || !archived {
		t.Fatalf("restart-only lifecycle archived=%t err=%v", archived, err)
	}
	if len(manager.pendingLifecycle) != 0 {
		t.Fatalf("restart-only lifecycle was left pending: %+v", manager.pendingLifecycle)
	}
	diskCfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !diskCfg.Transport.DownloadMedia || len(diskCfg.Groups) != 1 || !diskCfg.Groups[0].Archived {
		t.Fatalf("disk config lost restart-only change or archive: %+v", diskCfg)
	}
	liveCfg := holder.Load()
	if liveCfg.Transport.DownloadMedia || len(liveCfg.Groups) != 1 || !liveCfg.Groups[0].Archived {
		t.Fatalf("live config did not isolate restart-only change while archiving: %+v", liveCfg)
	}
}

func TestRunConfigManagerRetriesQueuedLifecycleThroughRestartOnlyDiskChange(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	initial.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-1",
		Enabled:         true,
		RelayManaged:    true,
	}}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	target := &retryingRunConfigTarget{}
	manager := newRunConfigManager(path, "", newRunConfigHolder(runtimeConfig), target, store, nil, nil)
	manager.pendingLifecycle = []types.GroupEvent{{
		ChatID:    "mrf-1@g.us",
		Deleted:   true,
		Timestamp: time.Now(),
	}}
	restartOnly := runtimeConfig
	restartOnly.Transport.DownloadMedia = true
	if err := config.Save(path, restartOnly); err != nil {
		t.Fatal(err)
	}

	changed, err := manager.Refresh(ctx)
	if err == nil || changed {
		t.Fatalf("restart-only refresh changed=%t err=%v", changed, err)
	}
	if len(manager.pendingLifecycle) != 0 {
		t.Fatalf("queued lifecycle was not drained: %+v", manager.pendingLifecycle)
	}
	diskCfg, loadErr := config.Load(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !diskCfg.Transport.DownloadMedia || !relayGroupArchived(diskCfg, "mrf-1@g.us") {
		t.Fatalf("disk config lost restart-only change or archive: %+v", diskCfg)
	}
	liveCfg := manager.holder.Load()
	if liveCfg.Transport.DownloadMedia || !relayGroupArchived(liveCfg, "mrf-1@g.us") {
		t.Fatalf("live config did not isolate restart-only change while archiving: %+v", liveCfg)
	}
}

func TestRunConfigManagerRetriesCleanupAfterInterruptedLifecyclePublication(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	initial.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-1",
		Enabled:         true,
		RelayManaged:    true,
	}}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	target := &interruptingRunConfigTarget{}
	manager := newRunConfigManager(path, "", newRunConfigHolder(runtimeConfig), target, store, nil, nil)
	archived, err := manager.HandleRelayGroupLifecycleEvent(ctx, types.GroupEvent{
		ChatID:    "mrf-1@g.us",
		Deleted:   true,
		Timestamp: time.Now(),
	})
	if err == nil || !archived || len(manager.pendingLifecycle) != 1 || !manager.generationDrainPending {
		t.Fatalf("interrupted lifecycle archived=%t err=%v pending=%+v drain_pending=%t", archived, err, manager.pendingLifecycle, manager.generationDrainPending)
	}
	if _, _, err := store.StoreActiveInboxMessage(ctx, initial.App.Profile, "mrf-1", "mrf-1", types.IncomingMessage{
		ID:        "late-old-generation",
		ChatID:    "mrf-1@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "owner@lid",
		Text:      "late write",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	err = manager.retryPendingLifecycleLocked(ctx)
	manager.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListActiveInbox(ctx, initial.App.Profile, "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 || len(manager.pendingLifecycle) != 0 || manager.generationDrainPending || target.setCount != 2 {
		t.Fatalf("cleanup retry rows=%+v pending=%+v drain_pending=%t set_count=%d", rows, manager.pendingLifecycle, manager.generationDrainPending, target.setCount)
	}
}

type retryingRunConfigTarget struct {
	setCount       int
	scheduleCount  int
	failSchedules  int
	lastConfigured config.Config
}

type interruptingRunConfigTarget struct {
	setCount      int
	scheduleCount int
}

func (t *interruptingRunConfigTarget) SetConfigAndWait(context.Context, config.Config) error {
	t.setCount++
	if t.setCount == 1 {
		return context.DeadlineExceeded
	}
	return nil
}

func (t *interruptingRunConfigTarget) ScheduleUnreadActiveFallbacks(context.Context, *config.Config, int) (int, error) {
	t.scheduleCount++
	return 0, nil
}

type blockingRunConfigTarget struct {
	published chan struct{}
	release   chan struct{}
}

func (t *blockingRunConfigTarget) SetConfigAndWait(ctx context.Context, _ config.Config) error {
	close(t.published)
	select {
	case <-t.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *blockingRunConfigTarget) ScheduleUnreadActiveFallbacks(context.Context, *config.Config, int) (int, error) {
	return 0, nil
}

func (t *retryingRunConfigTarget) SetConfigAndWait(_ context.Context, cfg config.Config) error {
	t.setCount++
	t.lastConfigured = cfg
	return nil
}

func (t *retryingRunConfigTarget) ScheduleUnreadActiveFallbacks(context.Context, *config.Config, int) (int, error) {
	t.scheduleCount++
	if t.failSchedules > 0 {
		t.failSchedules--
		return 0, fmt.Errorf("injected schedule failure")
	}
	return 0, nil
}

func TestRunConfigManagerRetriesPostPublishReconciliation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	holder := newRunConfigHolder(initial)
	target := &retryingRunConfigTarget{failSchedules: 1}
	manager := newRunConfigManager(path, "", holder, target, store, nil, nil)
	updated := initial
	updated.Active.AckMode = "verbose"
	if err := config.Save(path, updated); err != nil {
		t.Fatal(err)
	}

	changed, err := manager.Refresh(ctx)
	if err == nil || !changed {
		t.Fatalf("first refresh changed=%t err=%v", changed, err)
	}
	if !manager.reconcilePending || holder.Load().Active.AckMode != "verbose" || target.setCount != 1 {
		t.Fatalf("post-publish state pending=%t holder=%q set_count=%d", manager.reconcilePending, holder.Load().Active.AckMode, target.setCount)
	}
	changed, err = manager.Refresh(ctx)
	if err != nil || changed {
		t.Fatalf("reconciliation retry changed=%t err=%v", changed, err)
	}
	if manager.reconcilePending || target.scheduleCount != 2 || target.setCount != 1 {
		t.Fatalf("reconciliation retry pending=%t schedules=%d sets=%d", manager.reconcilePending, target.scheduleCount, target.setCount)
	}
}

func TestRunConfigManagerResumesInterruptedGenerationDrain(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	target := &interruptingRunConfigTarget{}
	manager := newRunConfigManager(path, "", newRunConfigHolder(runtimeConfig), target, store, nil, nil)
	updated := runtimeConfig
	updated.Active.AckMode = "verbose"
	if err := config.Save(path, updated); err != nil {
		t.Fatal(err)
	}

	changed, err := manager.Refresh(ctx)
	if err == nil || !changed || !manager.generationDrainPending || target.setCount != 1 {
		t.Fatalf("interrupted refresh changed=%t err=%v drain_pending=%t set_count=%d", changed, err, manager.generationDrainPending, target.setCount)
	}
	changed, err = manager.Refresh(ctx)
	if err != nil || changed || manager.generationDrainPending || manager.reconcilePending || target.setCount != 2 || target.scheduleCount != 1 {
		t.Fatalf("resumed refresh changed=%t err=%v drain_pending=%t reconcile_pending=%t sets=%d schedules=%d", changed, err, manager.generationDrainPending, manager.reconcilePending, target.setCount, target.scheduleCount)
	}
}

func TestRunConfigManagerDoesNotPublishBeforeBindingReconciliation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	holder := newRunConfigHolder(initial)
	target := &retryingRunConfigTarget{}
	manager := newRunConfigManager(path, "", holder, target, store, nil, nil)
	updated := initial
	updated.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "mrf-1",
		Enabled:         true,
	}}
	if err := config.Save(path, updated); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	changed, err := manager.Refresh(ctx)
	if err == nil || changed {
		t.Fatalf("failed reconciliation refresh changed=%t err=%v", changed, err)
	}
	if len(holder.Load().Groups) != 0 || target.setCount != 0 {
		t.Fatalf("unreconciled config was published: groups=%+v set_count=%d", holder.Load().Groups, target.setCount)
	}
}

func TestRunConfigManagerRepairsWritesFromDrainedGeneration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := config.Default()
	initial.App.Profile = "test"
	initial.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	initial.Transport.Type = "fake"
	initial.Groups = []config.GroupConfig{{
		ID:              "mrf-1@g.us",
		Alias:           "mrf-1",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "old-session",
		Enabled:         true,
	}}
	if err := config.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(initial.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, initial.App.Profile); err != nil {
		t.Fatal(err)
	}
	target := &blockingRunConfigTarget{published: make(chan struct{}), release: make(chan struct{})}
	holder := newRunConfigHolder(runtimeConfig)
	manager := newRunConfigManager(path, "", holder, target, store, nil, nil)
	updated := runtimeConfig
	updated.Groups = slices.Clone(runtimeConfig.Groups)
	updated.Groups[0].ActiveSessionID = "new-session"
	if err := config.Save(path, updated); err != nil {
		t.Fatal(err)
	}
	refreshDone := make(chan error, 1)
	go func() {
		_, err := manager.Refresh(ctx)
		refreshDone <- err
	}()
	select {
	case <-target.published:
	case <-time.After(2 * time.Second):
		t.Fatal("updated config generation was not published")
	}
	if _, _, err := store.StoreActiveInboxMessage(ctx, initial.App.Profile, "mrf-1", "old-session", types.IncomingMessage{
		ID:        "late-old-generation",
		ChatID:    "mrf-1@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "owner@lid",
		Text:      "late write",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	close(target.release)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListActiveInbox(ctx, initial.App.Profile, "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SessionID != "new-session" {
		t.Fatalf("late previous-generation row was not repaired: %+v", rows)
	}
}

func TestRunConfigManagerAppliesRuntimeProfileOverride(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	diskConfig := config.Default()
	diskConfig.App.Profile = "disk-profile"
	diskConfig.App.DatabasePath = filepath.Join(dir, "coderoam.sqlite3")
	diskConfig.Transport.Type = "fake"
	if err := config.Save(path, diskConfig); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := diskConfig
	runtimeConfig.App.Profile = "runtime-profile"
	store, err := db.Open(runtimeConfig.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, runtimeConfig.App.Profile); err != nil {
		t.Fatal(err)
	}
	holder := newRunConfigHolder(runtimeConfig)
	target := &retryingRunConfigTarget{}
	manager := newRunConfigManager(path, "runtime-profile", holder, target, store, nil, nil)
	updated := diskConfig
	updated.Active.AckMode = "verbose"
	if err := config.Save(path, updated); err != nil {
		t.Fatal(err)
	}

	changed, err := manager.Refresh(ctx)
	if err != nil || !changed {
		t.Fatalf("profile override refresh changed=%t err=%v", changed, err)
	}
	loaded := holder.Load()
	if loaded.App.Profile != "runtime-profile" || loaded.Active.AckMode != "verbose" {
		t.Fatalf("runtime config after reload = %+v", loaded)
	}
}

type runHandlerFunc func(context.Context, types.IncomingMessage) router.ProcessResult

func (f runHandlerFunc) Handle(ctx context.Context, msg types.IncomingMessage) router.ProcessResult {
	return f(ctx, msg)
}

func TestRunMessageDispatcherDoesNotBlockOtherSessions(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Concurrency.GlobalMaxInflight = 2
	cfg.Concurrency.QueueMaxDepthPerGroup = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sessionAStarted := make(chan struct{})
	releaseSessionA := make(chan struct{})
	sessionBHandled := make(chan struct{})
	handler := runHandlerFunc(func(ctx context.Context, msg types.IncomingMessage) router.ProcessResult {
		switch msg.ChatID {
		case "session-a@g.us":
			close(sessionAStarted)
			select {
			case <-releaseSessionA:
			case <-ctx.Done():
			}
		case "session-b@g.us":
			close(sessionBHandled)
		}
		return router.ProcessResult{Reason: "processed"}
	})
	dispatcher := newRunMessageDispatcher(ctx, handler, cfg, nil)
	defer dispatcher.Stop()

	if !dispatcher.Dispatch(types.IncomingMessage{ID: "a-1", ChatID: "session-a@g.us"}) {
		t.Fatal("session-a dispatch was rejected")
	}
	select {
	case <-sessionAStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("session-a handler did not start")
	}

	if !dispatcher.Dispatch(types.IncomingMessage{ID: "b-1", ChatID: "session-b@g.us"}) {
		t.Fatal("session-b dispatch was rejected")
	}
	select {
	case <-sessionBHandled:
	case <-time.After(2 * time.Second):
		t.Fatal("session-b was blocked behind session-a")
	}
	close(releaseSessionA)
}

func TestRunMessageDispatcherPreservesOrderWithinSession(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Concurrency.GlobalMaxInflight = 4
	cfg.Concurrency.QueueMaxDepthPerGroup = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	handler := runHandlerFunc(func(ctx context.Context, msg types.IncomingMessage) router.ProcessResult {
		switch msg.ID {
		case "first":
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
			}
		case "second":
			close(secondStarted)
		}
		return router.ProcessResult{Reason: "processed"}
	})
	dispatcher := newRunMessageDispatcher(ctx, handler, cfg, nil)
	defer dispatcher.Stop()

	if !dispatcher.Dispatch(types.IncomingMessage{ID: "first", ChatID: "session-a@g.us"}) {
		t.Fatal("first dispatch was rejected")
	}
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first message did not start")
	}
	if !dispatcher.Dispatch(types.IncomingMessage{ID: "second", ChatID: "session-a@g.us"}) {
		t.Fatal("second dispatch was rejected")
	}
	select {
	case <-secondStarted:
		t.Fatal("second message started before first message finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second message did not start after first finished")
	}
}

func TestRunMessageDispatcherDropsOldestQueuedMessageOnOverflow(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Concurrency.GlobalMaxInflight = 1
	cfg.Concurrency.QueueMaxDepthPerGroup = 1
	cfg.Concurrency.QueueOverflowPolicy = "drop_oldest_with_notice"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	handled := make(chan string, 3)
	handler := runHandlerFunc(func(ctx context.Context, msg types.IncomingMessage) router.ProcessResult {
		if msg.ID == "first" {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
			}
		}
		handled <- msg.ID
		return router.ProcessResult{Reason: "processed"}
	})
	var logsMu sync.Mutex
	var logs []string
	dispatcher := newRunMessageDispatcher(ctx, handler, cfg, func(format string, args ...any) {
		logsMu.Lock()
		defer logsMu.Unlock()
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	defer dispatcher.Stop()

	if !dispatcher.Dispatch(types.IncomingMessage{ID: "first", ChatID: "session-a@g.us"}) {
		t.Fatal("first dispatch was rejected")
	}
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first message did not start")
	}
	if !dispatcher.Dispatch(types.IncomingMessage{ID: "second", ChatID: "session-a@g.us"}) {
		t.Fatal("second dispatch was rejected")
	}
	if !dispatcher.Dispatch(types.IncomingMessage{ID: "third", ChatID: "session-a@g.us"}) {
		t.Fatal("third dispatch was rejected after dropping oldest queued message")
	}
	close(releaseFirst)

	gotFirst := waitForHandledID(t, handled)
	gotSecond := waitForHandledID(t, handled)
	if gotFirst != "first" || gotSecond != "third" {
		t.Fatalf("handled ids = %q, %q; want first, third", gotFirst, gotSecond)
	}
	select {
	case id := <-handled:
		t.Fatalf("unexpected handled id after queue overflow: %s", id)
	case <-time.After(100 * time.Millisecond):
	}
	logsMu.Lock()
	joinedLogs := strings.Join(logs, "\n")
	logsMu.Unlock()
	if !strings.Contains(joinedLogs, "message queue overflow dropped oldest") {
		t.Fatalf("dispatcher logs = %q, want overflow drop notice", joinedLogs)
	}
}

func waitForHandledID(t *testing.T, handled <-chan string) string {
	t.Helper()
	select {
	case id := <-handled:
		return id
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handled message")
	}
	return ""
}

type blockingSendTransport struct {
	transport.ChatTransport

	sessionAStarted chan struct{}
	releaseSessionA chan struct{}
	sessionBSent    chan struct{}
}

func (t *blockingSendTransport) SendText(ctx context.Context, chatID string, text string, opts types.SendOptions) (*types.SentMessage, error) {
	switch chatID {
	case "session-a@g.us":
		close(t.sessionAStarted)
		select {
		case <-t.releaseSessionA:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case "session-b@g.us":
		close(t.sessionBSent)
	}
	return &types.SentMessage{ID: "sent-" + chatID, ChatID: chatID, SentAt: time.Now()}, nil
}

func TestSendPendingActiveOutboxDoesNotBlockOtherSessions(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Concurrency.GlobalMaxInflight = 2
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.QueueActiveOutbox(t.Context(), cfg.App.Profile, "session-a@g.us", "blocked", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.QueueActiveOutbox(t.Context(), cfg.App.Profile, "session-b@g.us", "should send", true); err != nil {
		t.Fatal(err)
	}
	transport := &blockingSendTransport{
		sessionAStarted: make(chan struct{}),
		releaseSessionA: make(chan struct{}),
		sessionBSent:    make(chan struct{}),
	}

	done := make(chan error, 1)
	go func() {
		sent, err := sendPendingActiveOutbox(context.Background(), store, transport, cfg, 10)
		if err == nil && sent != 2 {
			err = fmt.Errorf("sent = %d, want 2", sent)
		}
		done <- err
	}()

	select {
	case <-transport.sessionAStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("session-a send did not start")
	}
	select {
	case <-transport.sessionBSent:
	case <-time.After(2 * time.Second):
		t.Fatal("session-b send was blocked behind session-a")
	}
	close(transport.releaseSessionA)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("outbox send did not finish")
	}
}

type reconnectTrackingTransport struct {
	transport.ChatTransport

	mu           sync.Mutex
	connected    bool
	connectCalls int
	statusErr    error
	connectErr   error
}

func (t *reconnectTrackingTransport) Status(ctx context.Context) (*types.ConnectionStatus, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.statusErr != nil {
		return nil, t.statusErr
	}
	return &types.ConnectionStatus{Connected: t.connected, Account: "fake"}, nil
}

func (t *reconnectTrackingTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connectCalls++
	if t.connectErr != nil {
		return t.connectErr
	}
	t.connected = true
	return nil
}

func (t *reconnectTrackingTransport) calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connectCalls
}

func TestEnsureRunTransportConnectedReconnectsDisconnectedTransport(t *testing.T) {
	t.Parallel()
	transport := &reconnectTrackingTransport{}

	reconnected, err := ensureRunTransportConnected(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	if !reconnected {
		t.Fatal("expected disconnected transport to reconnect")
	}
	if transport.calls() != 1 {
		t.Fatalf("connect calls = %d, want 1", transport.calls())
	}

	reconnected, err = ensureRunTransportConnected(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	if reconnected {
		t.Fatal("already-connected transport should not reconnect")
	}
	if transport.calls() != 1 {
		t.Fatalf("connect calls = %d, want still 1", transport.calls())
	}
}

func TestEnsureRunTransportConnectedReconnectsWhenStatusFails(t *testing.T) {
	t.Parallel()
	transport := &reconnectTrackingTransport{statusErr: fmt.Errorf("status unavailable")}

	reconnected, err := ensureRunTransportConnected(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	if !reconnected || transport.calls() != 1 {
		t.Fatalf("reconnected=%t calls=%d, want reconnect once", reconnected, transport.calls())
	}
}
