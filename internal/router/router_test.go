package router

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endurantdevs/codex-whatsapp/internal/config"
	"github.com/endurantdevs/codex-whatsapp/internal/db"
	"github.com/endurantdevs/codex-whatsapp/internal/runner"
	"github.com/endurantdevs/codex-whatsapp/internal/transport/fake"
	"github.com/endurantdevs/codex-whatsapp/internal/types"
)

func TestRouterProcessesAllowedTriggeredMessageAndDedupes(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Runner["default"] = config.RunnerConfig{
		Mode:    "process-once-json",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestRouterHelperProcess", "--", "json"},
		Env:     map[string]string{"GO_WANT_ROUTER_HELPER_PROCESS": "1"},
	}
	cfg.Groups = []config.GroupConfig{{ID: "1203630test@g.us", Alias: "test", Runner: "default", Enabled: true}}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	msg := types.IncomingMessage{
		ID:         "msg-1",
		ChatID:     "1203630test@g.us",
		ChatType:   types.ChatTypeGroup,
		ChatName:   "Test Group",
		SenderID:   "380506171414@s.whatsapp.net",
		SenderName: "Nick",
		Text:       "ping",
		RawText:    "@bridge ping",
		Timestamp:  time.Now(),
	}
	result := r.Handle(t.Context(), msg)
	if result.Ignored {
		t.Fatalf("message was ignored: %s", result.Reason)
	}
	if len(ft.Sent) != 1 {
		t.Fatalf("sent count = %d", len(ft.Sent))
	}
	if got := ft.Sent[0].Text; got != "router: ping" {
		t.Fatalf("reply text = %q", got)
	}
	if len(ft.Read) != 1 {
		t.Fatalf("read receipt count = %d, want 1", len(ft.Read))
	}
	if ft.Read[0].MessageID != "msg-1" {
		t.Fatalf("read receipt message id = %q", ft.Read[0].MessageID)
	}

	duplicate := r.Handle(t.Context(), msg)
	if !duplicate.Ignored || !strings.Contains(duplicate.Reason, "duplicate") {
		t.Fatalf("duplicate result = %+v", duplicate)
	}
	if len(ft.Sent) != 1 {
		t.Fatalf("duplicate sent count = %d", len(ft.Sent))
	}
	if len(ft.Read) != 1 {
		t.Fatalf("duplicate read receipt count = %d, want 1", len(ft.Read))
	}
}

func TestRouterIgnoresUnallowedGroup(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	result := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-2",
		ChatID:    "not-allowed@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "ping",
		RawText:   "@bridge ping",
		Timestamp: time.Now(),
	})
	if !result.Ignored || result.Reason != "chat is not allowlisted" {
		t.Fatalf("result = %+v", result)
	}
	if len(ft.Sent) != 0 {
		t.Fatalf("sent count = %d", len(ft.Sent))
	}
}

func TestRouterStoresActiveSessionMessageWithoutRunner(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Groups = []config.GroupConfig{{
		ID:      "1203630active@g.us",
		Alias:   "codex-session",
		Mode:    config.GroupModeActiveSession,
		Enabled: true,
	}}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	msg := types.IncomingMessage{
		ID:         "msg-active-1",
		ChatID:     "1203630active@g.us",
		ChatType:   types.ChatTypeGroup,
		ChatName:   "Codex Session",
		SenderID:   "380506171414@s.whatsapp.net",
		SenderName: "Nick",
		Text:       "status",
		RawText:    "status",
		Timestamp:  time.Now(),
	}
	result := r.Handle(t.Context(), msg)
	if result.Ignored {
		t.Fatalf("message was ignored: %s", result.Reason)
	}
	if len(ft.Sent) != 0 {
		t.Fatalf("sent count = %d", len(ft.Sent))
	}
	rows, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("active inbox count = %d, want 1", len(rows))
	}
	if rows[0].Text != "status" || rows[0].ChatAlias != "codex-session" || rows[0].SessionID != "codex-session" {
		t.Fatalf("active inbox row = %+v", rows[0])
	}
	pendingOutbox, err := store.PendingActiveOutbox(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingOutbox) != 1 {
		t.Fatalf("pending active outbox count = %d, want 1", len(pendingOutbox))
	}
	if !strings.Contains(pendingOutbox[0].Text, "for session codex-session") || !strings.Contains(pendingOutbox[0].Text, "Waiting for that active Codex session to claim") {
		t.Fatalf("ack text = %q", pendingOutbox[0].Text)
	}
	if len(ft.Read) != 0 {
		t.Fatalf("active-session should not mark read before claim; count = %d", len(ft.Read))
	}
	duplicate := r.Handle(t.Context(), msg)
	if !duplicate.Ignored || !strings.Contains(duplicate.Reason, "duplicate active inbox") {
		t.Fatalf("duplicate result = %+v", duplicate)
	}
	pendingOutbox, err = store.PendingActiveOutbox(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingOutbox) != 1 {
		t.Fatalf("duplicate ack count = %d, want 1", len(pendingOutbox))
	}
	if len(ft.Read) != 0 {
		t.Fatalf("duplicate active-session read receipt count = %d, want 0", len(ft.Read))
	}
}

func TestRouterActiveSessionFallsBackToRunnerWithoutWatcher(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Runner["codex-active"] = config.RunnerConfig{
		Mode:    "process-once-json",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestRouterHelperProcess", "--", "json"},
		Env:     map[string]string{"GO_WANT_ROUTER_HELPER_PROCESS": "1"},
	}
	cfg.Groups = []config.GroupConfig{{
		ID:      "1203630active@g.us",
		Alias:   "codex-session",
		Runner:  "codex-active",
		Mode:    config.GroupModeActiveSession,
		Enabled: true,
	}}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	result := r.Handle(t.Context(), types.IncomingMessage{
		ID:         "msg-active-fallback",
		ChatID:     "1203630active@g.us",
		ChatType:   types.ChatTypeGroup,
		ChatName:   "Codex Session",
		SenderID:   "380506171414@s.whatsapp.net",
		SenderName: "Nick",
		Text:       "continue",
		RawText:    "continue",
		Timestamp:  time.Now(),
	})
	if result.Ignored {
		t.Fatalf("message was ignored: %s", result.Reason)
	}
	if result.Reason != "active inbox fallback runner processed" {
		t.Fatalf("reason = %q", result.Reason)
	}
	if len(ft.Sent) != 1 || ft.Sent[0].Text != "router: continue" {
		t.Fatalf("sent = %+v", ft.Sent)
	}
	done, err := store.ListActiveInbox(t.Context(), "test", "done", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0].ClaimedBySessionID != "codex-session" {
		t.Fatalf("done active inbox = %+v", done)
	}
	receipts, err := store.PendingActiveReadReceipts(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].ExternalMessageID != "msg-active-fallback" {
		t.Fatalf("read receipts = %+v", receipts)
	}
	pendingOutbox, err := store.PendingActiveOutbox(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingOutbox) != 1 || !strings.Contains(pendingOutbox[0].Text, "No live watcher is connected") {
		t.Fatalf("pending active outbox = %+v", pendingOutbox)
	}
}

func TestRouterActiveSessionDoesNotFallbackWithFreshWatcher(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Runner["codex-active"] = config.RunnerConfig{
		Mode:    "process-once-json",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestRouterHelperProcess", "--", "json"},
		Env:     map[string]string{"GO_WANT_ROUTER_HELPER_PROCESS": "1"},
	}
	cfg.Groups = []config.GroupConfig{{
		ID:      "1203630active@g.us",
		Alias:   "codex-session",
		Runner:  "codex-active",
		Mode:    config.GroupModeActiveSession,
		Enabled: true,
	}}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, acquired, err := store.AcquireActiveWatcher(t.Context(), "test", "codex-session", "watcher", 123, 15*time.Second, false); err != nil {
		t.Fatal(err)
	} else if !acquired {
		t.Fatal("watcher was not acquired")
	}
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	result := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-active-watch",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "380506171414@s.whatsapp.net",
		Text:      "watcher gets this",
		RawText:   "watcher gets this",
		Timestamp: time.Now(),
	})
	if result.Ignored {
		t.Fatalf("message was ignored: %s", result.Reason)
	}
	if len(ft.Sent) != 0 {
		t.Fatalf("fallback sent with watcher connected: %+v", ft.Sent)
	}
	unread, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ExternalMessageID != "msg-active-watch" {
		t.Fatalf("unread = %+v", unread)
	}
}

func TestRouterActiveSessionSenderAllowlistAcceptsAdmins(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Security.RequireSenderAllowlist = true
	cfg.Security.AdminSenderIDs = []string{"admin@lid"}
	cfg.Groups = []config.GroupConfig{{
		ID:      "1203630active@g.us",
		Alias:   "codex-session",
		Mode:    config.GroupModeActiveSession,
		Enabled: true,
	}}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	result := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-admin-1",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "admin@lid",
		Text:      "/goal secure this",
		RawText:   "/goal secure this",
		Timestamp: time.Now(),
	})
	if result.Ignored {
		t.Fatalf("admin sender was ignored: %s", result.Reason)
	}
	rows, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("active inbox count = %d, want 1", len(rows))
	}

	ignored := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-other-1",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "other@lid",
		Text:      "/goal do not run",
		RawText:   "/goal do not run",
		Timestamp: time.Now(),
	})
	if !ignored.Ignored || ignored.Reason != "sender is not allowlisted" {
		t.Fatalf("unauthorized result = %+v", ignored)
	}
}

func TestRouterHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ROUTER_HELPER_PROCESS") != "1" {
		return
	}
	body, _ := io.ReadAll(os.Stdin)
	var req runner.Request
	_ = json.Unmarshal(body, &req)
	_ = json.NewEncoder(os.Stdout).Encode(runner.Response{
		Version:   runner.ProtocolVersion,
		RequestID: req.RequestID,
		Actions:   []runner.Action{{Type: "reply", Text: "router: " + req.Text}},
	})
	os.Exit(0)
}
