package app

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/endurantdevs/codex-whatsapp/internal/config"
	"github.com/endurantdevs/codex-whatsapp/internal/db"
	"github.com/endurantdevs/codex-whatsapp/internal/transport/fake"
	"github.com/endurantdevs/codex-whatsapp/internal/types"
)

func TestBuildRunnerPresetEnablesImportantOnlyForResumableAssistants(t *testing.T) {
	tests := []struct {
		name         string
		sessionID    string
		importantKey string
		markerKey    string
	}{
		{
			name:         "codex-active",
			importantKey: "CODEX_RUNNER_IMPORTANT_ONLY",
			markerKey:    "CODEX_RUNNER_IGNORE_MARKER",
		},
		{
			name:         "codex-session",
			sessionID:    "019e-session",
			importantKey: "CODEX_RUNNER_IMPORTANT_ONLY",
			markerKey:    "CODEX_RUNNER_IGNORE_MARKER",
		},
		{
			name:         "claude",
			importantKey: "CLAUDE_RUNNER_IMPORTANT_ONLY",
			markerKey:    "CLAUDE_RUNNER_IGNORE_MARKER",
		},
		{
			name:         "claude-code",
			importantKey: "CLAUDE_RUNNER_IMPORTANT_ONLY",
			markerKey:    "CLAUDE_RUNNER_IGNORE_MARKER",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, err := buildRunnerPreset(tt.name, t.TempDir(), 120, "", "", tt.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if got := runner.Env[tt.importantKey]; got != "true" {
				t.Fatalf("%s = %q, want true", tt.importantKey, got)
			}
			if got := runner.Env[tt.markerKey]; got != "[[chat-bridge-ignore]]" {
				t.Fatalf("%s = %q, want default marker", tt.markerKey, got)
			}
		})
	}
}

func TestSendPendingActiveOutboxUsesBridgeTransport(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id, err := store.QueueActiveOutbox(t.Context(), cfg.App.Profile, "chat@g.us", "important update", true)
	if err != nil {
		t.Fatal(err)
	}
	ft := fake.New(nil)
	sent, err := sendPendingActiveOutbox(t.Context(), store, ft, cfg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if len(ft.Sent) != 1 || ft.Sent[0].Text != "important update" {
		t.Fatalf("fake sent = %+v", ft.Sent)
	}
	pending, err := store.PendingActiveOutbox(t.Context(), cfg.App.Profile, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending count = %d, want 0", len(pending))
	}
	if id == 0 {
		t.Fatal("queued outbox id was zero")
	}
}

func TestSendPendingActiveReadReceiptsUsesBridgeTransport(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:       "wa-read",
		ChatID:   "chat@g.us",
		SenderID: "sender@s.whatsapp.net",
		Text:     "hello",
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), cfg.App.Profile, "codex-session", "codex-session", msg); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), cfg.App.Profile, "codex-session"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("expected claimed row")
	}
	ft := fake.New(nil)
	sent, err := sendPendingActiveReadReceipts(t.Context(), store, ft, cfg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if len(ft.Read) != 1 || ft.Read[0].MessageID != "wa-read" {
		t.Fatalf("fake read receipts = %+v", ft.Read)
	}
	pending, err := store.PendingActiveReadReceipts(t.Context(), cfg.App.Profile, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending read receipts = %d, want 0", len(pending))
	}
}

func TestWatchActiveInboxClaimsMatchingSessionJSONL(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msgA := types.IncomingMessage{
		ID:       "wa-session-a",
		ChatID:   "chat-a@g.us",
		SenderID: "sender@s.whatsapp.net",
		Text:     "hello session a",
		RawText:  "hello session a",
	}
	msgB := types.IncomingMessage{
		ID:       "wa-session-b",
		ChatID:   "chat-b@g.us",
		SenderID: "sender@s.whatsapp.net",
		Text:     "hello session b",
		RawText:  "hello session b",
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), cfg.App.Profile, "alias-a", "session-a", msgA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), cfg.App.Profile, "alias-b", "session-b", msgB); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = watchActiveInbox(t.Context(), store, cfg, inboxWatchOptions{
		SessionID:         "session-a",
		Format:            "jsonl",
		ConsumerID:        "test-consumer",
		PollInterval:      time.Millisecond,
		HeartbeatInterval: time.Millisecond,
		StaleAfter:        time.Second,
		MaxMessages:       1,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type               string `json:"type"`
		Text               string `json:"text"`
		SessionID          string `json:"session_id"`
		ClaimedBySessionID string `json:"claimed_by_session_id"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &event); err != nil {
		t.Fatalf("jsonl output %q: %v", stdout.String(), err)
	}
	if event.Type != "message" || event.Text != msgA.Text || event.SessionID != "session-a" || event.ClaimedBySessionID != "session-a" {
		t.Fatalf("event = %+v", event)
	}
	claimed, err := store.ListActiveInbox(t.Context(), cfg.App.Profile, "claimed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ExternalMessageID != msgA.ID {
		t.Fatalf("claimed = %+v", claimed)
	}
	unread, err := store.ListActiveInbox(t.Context(), cfg.App.Profile, "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ExternalMessageID != msgB.ID {
		t.Fatalf("unread = %+v", unread)
	}
	receipts, err := store.PendingActiveReadReceipts(t.Context(), cfg.App.Profile, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].ExternalMessageID != msgA.ID {
		t.Fatalf("read receipts = %+v", receipts)
	}
	watcher, err := store.GetActiveWatcher(t.Context(), cfg.App.Profile, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if watcher.Status != "stopped" {
		t.Fatalf("watcher status = %q, want stopped", watcher.Status)
	}
}

func TestParseInboxSlashCommandDetectsGoal(t *testing.T) {
	command, value, ok := parseInboxSlashCommand("/goal prepare project for publishing")
	if !ok {
		t.Fatal("expected slash command")
	}
	if command != "/goal" {
		t.Fatalf("command = %q, want /goal", command)
	}
	if value != "prepare project for publishing" {
		t.Fatalf("value = %q", value)
	}
}

func TestParseInboxSlashCommandIgnoresPlainText(t *testing.T) {
	_, _, ok := parseInboxSlashCommand("prepare project")
	if ok {
		t.Fatal("plain text should not be treated as slash command")
	}
}

func TestSlashCommandSenderAuthorization(t *testing.T) {
	cfg := config.Default()
	cfg.Security.AdminSenderIDs = []string{"admin@lid"}
	cfg.Security.AllowedSenderIDs = []string{"allowed@s.whatsapp.net"}
	if !isSlashCommandSenderAuthorized(cfg, "admin@lid") {
		t.Fatal("admin sender should be authorized")
	}
	if !isSlashCommandSenderAuthorized(cfg, "allowed@s.whatsapp.net") {
		t.Fatal("allowed sender should be authorized")
	}
	if isSlashCommandSenderAuthorized(cfg, "other@lid") {
		t.Fatal("unknown sender should not be authorized")
	}
}
