package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeAppNameUsesLegacyNameForChatBridgeBinary(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })

	os.Args = []string{"/tmp/chat-bridge"}
	if got := RuntimeAppName(); got != "chat-bridge" {
		t.Fatalf("RuntimeAppName() = %q, want chat-bridge", got)
	}
	if got := Default().App.DatabasePath; got != "chat-bridge.sqlite3" {
		t.Fatalf("Default database = %q, want chat-bridge.sqlite3", got)
	}
	if got := DefaultConfigPath(); !strings.Contains(got, "chat-bridge") {
		t.Fatalf("DefaultConfigPath() = %q, want legacy chat-bridge path", got)
	}
}

func TestRuntimeAppNameUsesCoderoamNameByDefault(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })

	os.Args = []string{"/tmp/coderoam"}
	if got := RuntimeAppName(); got != AppName {
		t.Fatalf("RuntimeAppName() = %q, want %s", got, AppName)
	}
	if got := Default().App.DatabasePath; got != "coderoam.sqlite3" {
		t.Fatalf("Default database = %q, want coderoam.sqlite3", got)
	}
	if got := DefaultConfigPath(); !strings.Contains(got, "coderoam") {
		t.Fatalf("DefaultConfigPath() = %q, want coderoam path", got)
	}
}

func TestApplyDefaultsActiveConfig(t *testing.T) {
	t.Parallel()
	cfg := Config{}
	ApplyDefaults(&cfg)
	if cfg.Active.FallbackDelaySeconds != 2 {
		t.Fatalf("fallback delay = %d, want 2", cfg.Active.FallbackDelaySeconds)
	}
	if cfg.Active.FallbackBatchLimit != 8 {
		t.Fatalf("fallback batch limit = %d, want 8", cfg.Active.FallbackBatchLimit)
	}
	if cfg.Active.AckMode != "minimal" {
		t.Fatalf("ack mode = %q, want minimal", cfg.Active.AckMode)
	}
}

func TestApplyDefaultsNormalizesActiveConfig(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Active.FallbackDelaySeconds = -1
	cfg.Active.FallbackBatchLimit = 0
	cfg.Active.AckMode = "loud"
	ApplyDefaults(&cfg)
	if cfg.Active.FallbackDelaySeconds != 2 {
		t.Fatalf("fallback delay = %d, want 2", cfg.Active.FallbackDelaySeconds)
	}
	if cfg.Active.FallbackBatchLimit != 8 {
		t.Fatalf("fallback batch limit = %d, want 8", cfg.Active.FallbackBatchLimit)
	}
	if cfg.Active.AckMode != "minimal" {
		t.Fatalf("ack mode = %q, want minimal", cfg.Active.AckMode)
	}
}

func TestApplyDefaultsDisablesUnsupportedSessionEncryption(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Security.StoreSessionsEncrypted = true
	ApplyDefaults(&cfg)
	if cfg.Security.StoreSessionsEncrypted {
		t.Fatal("store_sessions_encrypted should normalize to false until encryption is implemented")
	}
}

func TestValidateRunnerAllowsProcessJSONL(t *testing.T) {
	t.Parallel()
	err := ValidateRunner("default", RunnerConfig{
		Mode:    "process-jsonl",
		Command: "/usr/bin/true",
	})
	if err != nil {
		t.Fatalf("ValidateRunner returned error: %v", err)
	}
}

func TestValidateActiveSessionBindingsRejectsOneSessionForMultipleChats(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Groups = []GroupConfig{
		{ID: "chat-a@g.us", Alias: "codex-a", Mode: GroupModeActiveSession, ActiveSessionID: "codex-session", Enabled: true},
		{ID: "chat-b@g.us", Alias: "codex-b", Mode: GroupModeActiveSession, ActiveSessionID: "codex-session", Enabled: true},
	}
	err := ValidateActiveSessionBindings(cfg)
	if err == nil || !strings.Contains(err.Error(), "active session id codex-session is configured for multiple chats") {
		t.Fatalf("error = %v, want duplicate session guard", err)
	}
}

func TestValidateActiveSessionBindingsRejectsOneAliasForMultipleChats(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Groups = []GroupConfig{
		{ID: "chat-a@g.us", Alias: "shared", Mode: GroupModeActiveSession, ActiveSessionID: "session-a", Enabled: true},
		{ID: "chat-b@g.us", Alias: "shared", Mode: GroupModeActiveSession, ActiveSessionID: "session-b", Enabled: true},
	}
	err := ValidateActiveSessionBindings(cfg)
	if err == nil || !strings.Contains(err.Error(), "active group alias shared is configured for multiple chats") {
		t.Fatalf("error = %v, want duplicate alias guard", err)
	}
}

func TestValidateActiveSessionBindingsRejectsOneChatForMultipleSessions(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Groups = []GroupConfig{
		{ID: "chat-a@g.us", Alias: "session-a", Mode: GroupModeActiveSession, ActiveSessionID: "session-a", Enabled: true},
		{ID: "chat-a@g.us", Alias: "session-b", Mode: GroupModeActiveSession, ActiveSessionID: "session-b", Enabled: true},
	}
	err := ValidateActiveSessionBindings(cfg)
	if err == nil || !strings.Contains(err.Error(), "chat chat-a@g.us is configured for multiple active sessions") {
		t.Fatalf("error = %v, want duplicate chat guard", err)
	}
}

func TestValidateActiveSessionBindingsIgnoresDisabledAndArchivedGroups(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Groups = []GroupConfig{
		{ID: "old-a@g.us", Alias: "codex-old", Mode: GroupModeActiveSession, ActiveSessionID: "codex-session", Enabled: false, RelayManaged: true, Archived: true},
		{ID: "chat-a@g.us", Alias: "codex", Mode: GroupModeActiveSession, ActiveSessionID: "codex-session", Enabled: true},
	}
	if err := ValidateActiveSessionBindings(cfg); err != nil {
		t.Fatalf("ValidateActiveSessionBindings returned error: %v", err)
	}
}

func TestSaveRejectsDuplicateActiveSessionBindings(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Groups = []GroupConfig{
		{ID: "chat-a@g.us", Alias: "codex-a", Mode: GroupModeActiveSession, ActiveSessionID: "codex-session", Enabled: true},
		{ID: "chat-b@g.us", Alias: "codex-b", Mode: GroupModeActiveSession, ActiveSessionID: "codex-session", Enabled: true},
	}
	err := Save(filepath.Join(t.TempDir(), "config.toml"), cfg)
	if err == nil || !strings.Contains(err.Error(), "active session id codex-session is configured for multiple chats") {
		t.Fatalf("error = %v, want duplicate session save guard", err)
	}
}
