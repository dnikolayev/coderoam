package app

import (
	"context"
	"fmt"
	"path/filepath"
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
