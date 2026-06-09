package router

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/runner"
	"github.com/dnikolayev/coderoam/internal/transport/fake"
	"github.com/dnikolayev/coderoam/internal/types"
)

func TestRouterProcessesAllowedTriggeredMessageAndDedupes(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	dbPath := filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.App.DatabasePath = dbPath
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

func TestRouterReusesProcessJSONLRunnerPerChatSession(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Runner["default"] = config.RunnerConfig{
		Mode:    "process-jsonl",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestRouterHelperProcess", "--", "jsonl"},
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
	t.Cleanup(func() {
		_ = r.Stop(t.Context())
	})
	for i, text := range []string{"one", "two"} {
		result := r.Handle(t.Context(), types.IncomingMessage{
			ID:        fmt.Sprintf("msg-jsonl-%d", i+1),
			ChatID:    "1203630test@g.us",
			ChatType:  types.ChatTypeGroup,
			SenderID:  "sender@s.whatsapp.net",
			Text:      text,
			RawText:   "@bridge " + text,
			Timestamp: time.Now(),
		})
		if result.Ignored {
			t.Fatalf("message %d ignored: %s", i+1, result.Reason)
		}
	}
	if len(ft.Sent) != 2 {
		t.Fatalf("sent count = %d", len(ft.Sent))
	}
	if ft.Sent[0].Text != "jsonl 1: one" || ft.Sent[1].Text != "jsonl 2: two" {
		t.Fatalf("sent replies = %+v", ft.Sent)
	}
}

func TestRouterIgnoresUnallowedGroup(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	dbPath := filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.App.DatabasePath = dbPath
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	r.activeFallbackDelay = 0
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
	if len(pendingOutbox) != 1 || !strings.Contains(pendingOutbox[0].Text, "Queued #") || !strings.Contains(pendingOutbox[0].Text, "waiting for the live watcher") {
		t.Fatalf("minimal ack mode should queue a waiting-for-watcher ack: %+v", pendingOutbox)
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
		t.Fatalf("duplicate queued active outbox = %+v", pendingOutbox)
	}
	if len(ft.Read) != 0 {
		t.Fatalf("duplicate active-session read receipt count = %d, want 0", len(ft.Read))
	}
}

func TestRouterRoutesParallelActiveSessionsToSeparateInboxes(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Groups = []config.GroupConfig{
		{
			ID:              "session-a@g.us",
			Alias:           "session-a",
			Mode:            config.GroupModeActiveSession,
			ActiveSessionID: "session-a",
			RelayManaged:    true,
			Enabled:         true,
		},
		{
			ID:              "session-b@g.us",
			Alias:           "session-b",
			Mode:            config.GroupModeActiveSession,
			ActiveSessionID: "session-b",
			RelayManaged:    true,
			Enabled:         true,
		},
	}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	messages := []types.IncomingMessage{
		{
			ID:        "wa-a-1",
			ChatID:    "session-a@g.us",
			ChatType:  types.ChatTypeGroup,
			SenderID:  "owner-a@s.whatsapp.net",
			Text:      "message for a",
			RawText:   "message for a",
			Timestamp: time.Now(),
		},
		{
			ID:        "wa-b-1",
			ChatID:    "session-b@g.us",
			ChatType:  types.ChatTypeGroup,
			SenderID:  "owner-b@s.whatsapp.net",
			Text:      "message for b",
			RawText:   "message for b",
			Timestamp: time.Now(),
		},
	}
	var wg sync.WaitGroup
	results := make([]ProcessResult, len(messages))
	for i := range messages {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = r.Handle(t.Context(), messages[i])
		}(i)
	}
	wg.Wait()
	for i, result := range results {
		if result.Ignored {
			t.Fatalf("message %d ignored: %+v", i, result)
		}
	}
	sessionB, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || sessionB.ExternalMessageID != "wa-b-1" || sessionB.ChatID != "session-b@g.us" || sessionB.SessionID != "session-b" || sessionB.ClaimedBySessionID != "session-b" {
		t.Fatalf("session-b claim = %+v ok=%t", sessionB, ok)
	}
	if _, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "session-b"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("session-b claimed a row from another session")
	}
	sessionA, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || sessionA.ExternalMessageID != "wa-a-1" || sessionA.ChatID != "session-a@g.us" || sessionA.SessionID != "session-a" || sessionA.ClaimedBySessionID != "session-a" {
		t.Fatalf("session-a claim = %+v ok=%t", sessionA, ok)
	}
	if len(ft.Sent) != 0 {
		t.Fatalf("active-session routing should not send WhatsApp replies before claim: %+v", ft.Sent)
	}
	if len(ft.Read) != 0 {
		t.Fatalf("active-session routing should defer read receipts until claim: %+v", ft.Read)
	}
}

func TestRouterActiveSessionVerboseAckQueuesWithoutRunner(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Active.AckMode = "verbose"
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
		ID:        "msg-active-verbose",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "380506171414@s.whatsapp.net",
		Text:      "status",
		RawText:   "status",
		Timestamp: time.Now(),
	})
	if result.Ignored {
		t.Fatalf("message was ignored: %s", result.Reason)
	}
	pendingOutbox, err := store.PendingActiveOutbox(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingOutbox) != 1 || !strings.Contains(pendingOutbox[0].Text, "Queued for the active Codex session to claim") {
		t.Fatalf("verbose ack = %+v", pendingOutbox)
	}
}

func TestRouterIgnoresRecentLongOutboxEcho(t *testing.T) {
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
	echoText := strings.Repeat("previous bridge reply ", 8)
	if err := store.RecordOutboxSent(t.Context(), "test", "1203630active@g.us", 123, echoText); err != nil {
		t.Fatal(err)
	}
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	r.activeFallbackDelay = 0
	result := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-echo",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "380506171414@s.whatsapp.net",
		Text:      echoText,
		RawText:   echoText,
		Timestamp: time.Now(),
	})
	if !result.Ignored || result.Reason != "recent outbox echo ignored" {
		t.Fatalf("echo result = %+v", result)
	}
	if len(ft.Sent) != 0 {
		t.Fatalf("echo should not send: %+v", ft.Sent)
	}
	rows, err := store.ListActiveInbox(t.Context(), "test", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("echo should not enter active inbox: %+v", rows)
	}
}

func TestRouterActiveSessionFallsBackToSafeRunnerWithoutWatcher(t *testing.T) {
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
	r.activeFallbackDelay = 0
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
	if len(done) != 1 || done[0].ExternalMessageID != "msg-active-fallback" || done[0].ClaimedBySessionID != "codex-session" {
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
	if len(pendingOutbox) != 0 {
		t.Fatalf("pending active outbox = %+v", pendingOutbox)
	}
}

func TestRouterActiveSessionFallbackCombinesUnreadBurst(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Runner["codex-active"] = config.RunnerConfig{
		Mode:    "process-once-json",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestRouterHelperProcess", "--", "json"},
		Env:     map[string]string{"GO_WANT_ROUTER_HELPER_PROCESS": "1"},
	}
	group := config.GroupConfig{
		ID:      "1203630active@g.us",
		Alias:   "codex-session",
		Runner:  "codex-active",
		Mode:    config.GroupModeActiveSession,
		Enabled: true,
	}
	cfg.Groups = []config.GroupConfig{group}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ft := fake.New(nil)
	r := New(cfg, store, ft)
	now := time.Now()
	for _, msg := range []types.IncomingMessage{
		{
			ID:         "msg-burst-1",
			ChatID:     "1203630active@g.us",
			ChatType:   types.ChatTypeGroup,
			SenderID:   "380506171414@s.whatsapp.net",
			SenderName: "Nick",
			Text:       "[voice] mime=audio/ogg; codecs=opus seconds=5 transcript=first part from voice",
			RawText:    "[voice] mime=audio/ogg; codecs=opus seconds=5 transcript=first part from voice",
			Media: []types.MediaAttachment{{
				Type:       "voice",
				MIMEType:   "audio/ogg; codecs=opus",
				LocalPath:  "/tmp/voice.ogg",
				Transcript: "first part from voice",
			}},
			Timestamp: now,
		},
		{
			ID:         "msg-burst-2",
			ChatID:     "1203630active@g.us",
			ChatType:   types.ChatTypeGroup,
			SenderID:   "380506171414@s.whatsapp.net",
			SenderName: "Nick",
			Text:       "second part",
			RawText:    "second part",
			Timestamp:  now.Add(time.Second),
		},
	} {
		if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", "codex-session", "codex-session", msg); err != nil {
			t.Fatal(err)
		}
	}
	result, err := r.processActiveSessionFallback(t.Context(), types.IncomingMessage{ChatID: "1203630active@g.us"}, group, "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if result.Ignored {
		t.Fatalf("fallback ignored burst: %s", result.Reason)
	}
	if len(ft.Sent) != 1 {
		t.Fatalf("sent = %+v", ft.Sent)
	}
	if !strings.Contains(ft.Sent[0].Text, "Multiple related WhatsApp messages") ||
		!strings.Contains(ft.Sent[0].Text, "first part from voice") ||
		!strings.Contains(ft.Sent[0].Text, "second part") {
		t.Fatalf("combined send text = %q", ft.Sent[0].Text)
	}
	done, err := store.ListActiveInbox(t.Context(), "test", "done", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 {
		t.Fatalf("done rows = %+v", done)
	}
	receipts, err := store.PendingActiveReadReceipts(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 2 {
		t.Fatalf("read receipts = %+v", receipts)
	}
}

func TestRouterActiveSessionScheduledFallbackDrainsLaterUnreadRows(t *testing.T) {
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
	r.activeFallbackDelay = 5 * time.Millisecond
	r.activeFallbackLimit = 1

	first := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-scheduled-1",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "380506171414@s.whatsapp.net",
		Text:      "first scheduled",
		RawText:   "first scheduled",
		Timestamp: time.Now(),
	})
	if first.Ignored || first.Reason != "active inbox fallback scheduled" {
		t.Fatalf("first result = %+v", first)
	}
	second := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-scheduled-2",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "380506171414@s.whatsapp.net",
		Text:      "second scheduled",
		RawText:   "second scheduled",
		Timestamp: time.Now().Add(time.Millisecond),
	})
	if second.Ignored || second.Reason != "active inbox fallback scheduled" {
		t.Fatalf("second result = %+v", second)
	}

	waitForRouterCondition(t, 10*time.Second, func() bool {
		done, err := store.ListActiveInbox(t.Context(), "test", "done", 10)
		if err != nil {
			t.Fatal(err)
		}
		return len(done) == 2
	})
	sent := ft.SentSnapshot()
	if len(sent) != 2 || sent[0].Text != "router: first scheduled" || sent[1].Text != "router: second scheduled" {
		t.Fatalf("sent = %+v", sent)
	}
	unread, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread rows left behind = %+v", unread)
	}
}

func TestRouterActiveSessionFallbackSkipsStaleClaimedRow(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	dbPath := filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.App.DatabasePath = dbPath
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
	previous := types.IncomingMessage{
		ID:        "msg-previous",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "380506171414@s.whatsapp.net",
		Text:      "previous",
		RawText:   "previous",
		Timestamp: time.Now().Add(-time.Minute),
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", "codex-session", "codex-session", previous); err != nil {
		t.Fatal(err)
	}
	claimedPrevious, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected previous row to be claimed")
	}
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	if _, err := rawDB.ExecContext(t.Context(), `UPDATE active_inbox SET claimed_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).Format(time.RFC3339Nano), claimedPrevious.ID); err != nil {
		t.Fatal(err)
	}

	ft := fake.New(nil)
	r := New(cfg, store, ft)
	r.activeFallbackDelay = 0
	result := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-current",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "380506171414@s.whatsapp.net",
		Text:      "current",
		RawText:   "current",
		Timestamp: time.Now(),
	})
	if result.Ignored {
		t.Fatalf("message was ignored: %s", result.Reason)
	}
	if len(ft.Sent) != 1 || ft.Sent[0].Text != "router: current" {
		t.Fatalf("sent = %+v", ft.Sent)
	}
	claimed, err := store.ListActiveInbox(t.Context(), "test", "claimed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ExternalMessageID != "msg-previous" {
		t.Fatalf("claimed rows = %+v", claimed)
	}
	done, err := store.ListActiveInbox(t.Context(), "test", "done", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0].ExternalMessageID != "msg-current" {
		t.Fatalf("done rows = %+v", done)
	}
}

func TestRouterActiveSessionQueuesPinnedSessionRunnerWithoutWatcher(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Runner["codex-session"] = config.RunnerConfig{
		Mode:    "process-once-json",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestRouterHelperProcess", "--", "json"},
		Env: map[string]string{
			"GO_WANT_ROUTER_HELPER_PROCESS": "1",
			"CODEX_RUNNER_SESSION_ID":       "019e9efc-2396-7da1-ad55-7cb73667a83d",
		},
	}
	cfg.Groups = []config.GroupConfig{{
		ID:      "1203630active@g.us",
		Alias:   "codex-session",
		Runner:  "codex-session",
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
		ID:        "msg-pinned-session",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "380506171414@s.whatsapp.net",
		Text:      "must not be swallowed",
		RawText:   "must not be swallowed",
		Timestamp: time.Now(),
	})
	if result.Ignored {
		t.Fatalf("message was ignored: %s", result.Reason)
	}
	if result.Reason != "active inbox queued" {
		t.Fatalf("reason = %q", result.Reason)
	}
	if len(ft.Sent) != 0 {
		t.Fatalf("pinned session fallback sent unexpectedly: %+v", ft.Sent)
	}
	unread, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ExternalMessageID != "msg-pinned-session" || unread[0].ClaimedBySessionID != "" {
		t.Fatalf("unread active inbox = %+v", unread)
	}
	receipts, err := store.PendingActiveReadReceipts(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 0 {
		t.Fatalf("read receipts = %+v", receipts)
	}
}

func TestRouterActiveSessionQueuesCodexResumeRunnerWithoutWatcher(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Runner["codex-active"] = config.RunnerConfig{
		Mode:    "process-once-json",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestRouterHelperProcess", "--", "json"},
		Env: map[string]string{
			"GO_WANT_ROUTER_HELPER_PROCESS": "1",
			"CODEX_RUNNER_RESUME":           "last",
			"CODEX_RUNNER_RESUME_ALL":       "true",
		},
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
		ID:        "msg-codex-resume",
		ChatID:    "1203630active@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "380506171414@s.whatsapp.net",
		Text:      "do not block whatsapp",
		RawText:   "do not block whatsapp",
		Timestamp: time.Now(),
	})
	if result.Ignored {
		t.Fatalf("message was ignored: %s", result.Reason)
	}
	if result.Reason != "active inbox queued" {
		t.Fatalf("reason = %q", result.Reason)
	}
	if len(ft.Sent) != 0 {
		t.Fatalf("codex resume fallback sent unexpectedly: %+v", ft.Sent)
	}
	unread, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ExternalMessageID != "msg-codex-resume" || unread[0].ClaimedBySessionID != "" {
		t.Fatalf("unread active inbox = %+v", unread)
	}
	receipts, err := store.PendingActiveReadReceipts(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 0 {
		t.Fatalf("read receipts = %+v", receipts)
	}
	pendingOutbox, err := store.PendingActiveOutbox(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingOutbox) != 1 || !strings.Contains(pendingOutbox[0].Text, "Queued #") {
		t.Fatalf("queued ack = %+v", pendingOutbox)
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

func TestRouterActiveSessionRoutesPendingChoiceReplyThroughSafeFallback(t *testing.T) {
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
	r.activeFallbackDelay = 0
	if _, err := store.CreatePendingInteraction(t.Context(), db.PendingInteractionRecord{
		ProfileID:       "test",
		ChatID:          "1203630active@g.us",
		SenderID:        "380506171414@s.whatsapp.net",
		RunnerID:        "codex-active",
		SourceMessageID: 42,
		Prompt:          "Choose next step.",
		Options:         []string{"Plan", "Continue"},
		ExpiresAt:       time.Now().Add(15 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	reply := r.Handle(t.Context(), types.IncomingMessage{
		ID:         "msg-active-choice-reply",
		ChatID:     "1203630active@g.us",
		ChatType:   types.ChatTypeGroup,
		ChatName:   "Codex Session",
		SenderID:   "380506171414@s.whatsapp.net",
		SenderName: "Nick",
		Text:       "2",
		RawText:    "2",
		Timestamp:  time.Now(),
	})
	if reply.Ignored {
		t.Fatalf("choice reply ignored: %s", reply.Reason)
	}
	if len(ft.Sent) != 1 || ft.Sent[0].Text != "router: Continue" {
		t.Fatalf("active-session choice reply fallback = %+v", ft.Sent)
	}
	if _, ok, err := store.FindPendingInteraction(t.Context(), "test", "1203630active@g.us", "380506171414@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("pending interaction still active after answer")
	}
}

func TestRouterPendingChoiceInvalidReplyKeepsInteraction(t *testing.T) {
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
	r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-choice",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "ask-choice",
		RawText:   "@bridge ask-choice",
		Timestamp: time.Now(),
	})
	result := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-choice-bad",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "9",
		RawText:   "9",
		Timestamp: time.Now(),
	})
	if result.Ignored || result.Reason != "pending interaction invalid choice" {
		t.Fatalf("invalid choice result = %+v", result)
	}
	if len(ft.Sent) != 2 || !strings.Contains(ft.Sent[1].Text, "I did not recognize that choice") {
		t.Fatalf("invalid choice reply = %+v", ft.Sent)
	}
	if _, ok, err := store.FindPendingInteraction(t.Context(), "test", "1203630test@g.us", "sender@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("pending interaction should remain active")
	}
}

func TestResolveInteractionReplyMatchesHumanLanguage(t *testing.T) {
	record := db.PendingInteractionRecord{Options: []string{
		"`LICENSE`, `NOTICE`, `THIRD_PARTY_LICENSES.md`",
		"`SECURITY.md` with credential/session risks and WhatsApp account-risk warning",
		"`PRIVACY.md` review: local storage, media, transcripts, deletion",
		"`CONTRIBUTING.md` + `CODE_OF_CONDUCT.md`",
		"GitHub issue templates and PR template",
		"CI: test, build, lint, dependency/license check",
		"Release workflow: goreleaser, checksums, signed artifacts later",
		"Clear README disclaimer: local personal use, unofficial WhatsApp transport, no spam/bulk/surveillance",
		"Architecture docs: transports, runner protocol, active-session relay",
		"Example runners and config presets",
	}}
	cases := []struct {
		text  string
		index int
	}{
		{text: "`LICENSE`, `NOTICE`, `THIRD_PARTY_LICENSES.md`", index: 1},
		{text: "Let's do SECURITY.md with the account-risk warning next", index: 2},
		{text: "privacy: local storage/media/transcripts/deletion", index: 3},
		{text: "contributing and code of conduct", index: 4},
		{text: "github issue templates", index: 5},
		{text: "CI please", index: 6},
		{text: "release workflow with checksums", index: 7},
		{text: "README disclaimer about no spam", index: 8},
		{text: "architecture docs", index: 9},
		{text: "example runners", index: 10},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			_, index, ok := resolveInteractionReply(record, tc.text)
			if !ok || index != tc.index {
				t.Fatalf("resolveInteractionReply(%q) = ok=%t index=%d, want ok=true index=%d", tc.text, ok, index, tc.index)
			}
		})
	}
}

func TestResolveInteractionReplyReportsAmbiguousHumanLanguage(t *testing.T) {
	record := db.PendingInteractionRecord{Options: []string{
		"Frontend documentation",
		"Backend documentation",
		"Release workflow",
	}}
	_, _, ok, ambiguous := resolveInteractionReplyDetail(record, "docs please")
	if ok {
		t.Fatal("ambiguous human reply should not resolve automatically")
	}
	if len(ambiguous) != 2 || ambiguous[0] != 1 || ambiguous[1] != 2 {
		t.Fatalf("ambiguous matches = %+v, want [1 2]", ambiguous)
	}
	reply := ambiguousInteractionReply(record, ambiguous)
	if !strings.Contains(reply, "That could mean more than one option") ||
		!strings.Contains(reply, "1. Frontend documentation") ||
		!strings.Contains(reply, "2. Backend documentation") ||
		strings.Contains(reply, "Release workflow") {
		t.Fatalf("ambiguous reply = %q", reply)
	}
}

func TestRouterPendingFreeTextAnswerRoutesToRunner(t *testing.T) {
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
	r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-input",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "ask-input",
		RawText:   "@bridge ask-input",
		Timestamp: time.Now(),
	})
	if len(ft.Sent) != 1 || !strings.Contains(ft.Sent[0].Text, "Reply with your answer") {
		t.Fatalf("free-text prompt = %+v", ft.Sent)
	}
	reply := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-input-reply",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "ship the text menu",
		RawText:   "ship the text menu",
		Timestamp: time.Now(),
	})
	if reply.Ignored {
		t.Fatalf("free-text reply ignored: %s", reply.Reason)
	}
	if len(ft.Sent) != 2 || ft.Sent[1].Text != "router: ship the text menu" {
		t.Fatalf("free-text answer response = %+v", ft.Sent)
	}
}

func TestRouterPendingApprovalUsesDefaultOptions(t *testing.T) {
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
	r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-approval",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "ask-approval",
		RawText:   "@bridge ask-approval",
		Timestamp: time.Now(),
	})
	if len(ft.Sent) != 1 || !strings.Contains(ft.Sent[0].Text, "1. Approve") || !strings.Contains(ft.Sent[0].Text, "2. Reject") {
		t.Fatalf("approval prompt = %+v", ft.Sent)
	}
	reply := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-approval-reply",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "1",
		RawText:   "1",
		Timestamp: time.Now(),
	})
	if reply.Ignored {
		t.Fatalf("approval reply ignored: %s", reply.Reason)
	}
	if len(ft.Sent) != 2 || ft.Sent[1].Text != "router: Approve" {
		t.Fatalf("approval response = %+v", ft.Sent)
	}
}

func TestRouterExpiredPendingInteractionDoesNotBypassTrigger(t *testing.T) {
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
	if _, err := store.CreatePendingInteraction(t.Context(), db.PendingInteractionRecord{
		ProfileID: "test",
		ChatID:    "1203630test@g.us",
		SenderID:  "sender@s.whatsapp.net",
		RunnerID:  "default",
		Prompt:    "Expired question.",
		Options:   []string{"Continue", "Stop"},
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	result := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-expired-reply",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "1",
		RawText:   "1",
		Timestamp: time.Now(),
	})
	if !result.Ignored || result.Reason != "trigger not matched" {
		t.Fatalf("expired reply result = %+v", result)
	}
	if len(ft.Sent) != 0 {
		t.Fatalf("expired interaction sent response = %+v", ft.Sent)
	}
}

func TestRouterDuplicatePendingReplyDoesNotRunTwice(t *testing.T) {
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
	if _, err := store.CreatePendingInteraction(t.Context(), db.PendingInteractionRecord{
		ProfileID: "test",
		ChatID:    "1203630test@g.us",
		SenderID:  "sender@s.whatsapp.net",
		RunnerID:  "default",
		Prompt:    "Choose next step.",
		Options:   []string{"Plan", "Continue"},
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	msg := types.IncomingMessage{
		ID:        "msg-choice-reply-dup",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "2",
		RawText:   "2",
		Timestamp: time.Now(),
	}
	first := r.Handle(t.Context(), msg)
	if first.Ignored {
		t.Fatalf("first reply ignored: %s", first.Reason)
	}
	second := r.Handle(t.Context(), msg)
	if !second.Ignored {
		t.Fatalf("duplicate reply was not ignored: %+v", second)
	}
	if len(ft.Sent) != 1 || ft.Sent[0].Text != "router: Continue" {
		t.Fatalf("duplicate reply sends = %+v", ft.Sent)
	}
}

func TestRouterDetectsNumberedQuestionAsPendingInteraction(t *testing.T) {
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
	r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-numbered",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "ask-numbered",
		RawText:   "@bridge ask-numbered",
		Timestamp: time.Now(),
	})
	if len(ft.Sent) != 1 || !strings.Contains(ft.Sent[0].Text, "1. Alpha") {
		t.Fatalf("numbered prompt = %+v", ft.Sent)
	}
	reply := r.Handle(t.Context(), types.IncomingMessage{
		ID:        "msg-numbered-reply",
		ChatID:    "1203630test@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "2",
		RawText:   "2",
		Timestamp: time.Now(),
	})
	if reply.Ignored {
		t.Fatalf("numbered reply ignored: %s", reply.Reason)
	}
	if len(ft.Sent) != 2 || ft.Sent[1].Text != "router: Beta" {
		t.Fatalf("sent after numbered reply = %+v", ft.Sent)
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

func waitForRouterCondition(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ok() {
		return
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestRouterHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ROUTER_HELPER_PROCESS") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "jsonl" {
		decoder := json.NewDecoder(os.Stdin)
		encoder := json.NewEncoder(os.Stdout)
		count := 0
		for {
			var req runner.Request
			if err := decoder.Decode(&req); err != nil {
				os.Exit(0)
			}
			count++
			_ = encoder.Encode(runner.Response{
				Version:   runner.ProtocolVersion,
				RequestID: req.RequestID,
				Actions:   []runner.Action{{Type: "reply", Text: fmt.Sprintf("jsonl %d: %s", count, req.Text)}},
			})
		}
	}
	body, _ := io.ReadAll(os.Stdin)
	var req runner.Request
	_ = json.Unmarshal(body, &req)
	actions := []runner.Action{{Type: "reply", Text: "router: " + req.Text}}
	switch req.Text {
	case "ask-choice":
		actions = []runner.Action{{
			Type:           "request_choice",
			Text:           "Choose how Codex should continue.",
			Options:        []string{"Plan", "Continue"},
			ExpiresSeconds: 60,
		}}
	case "ask-input":
		actions = []runner.Action{{Type: "request_input", Text: "What should I send back?"}}
	case "ask-approval":
		actions = []runner.Action{{Type: "request_approval", Text: "Approve the current plan?"}}
	case "ask-numbered":
		actions = []runner.Action{{Type: "reply", Text: "Choose one?\n1. Alpha\n2. Beta"}}
	}
	_ = json.NewEncoder(os.Stdout).Encode(runner.Response{
		Version:   runner.ProtocolVersion,
		RequestID: req.RequestID,
		Actions:   actions,
	})
	os.Exit(0)
}
