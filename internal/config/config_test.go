package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeAppNameUsesCoderoamForOldBinaryNames(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })

	os.Args = []string{"/tmp/old-binary-name"}
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

func TestValidateActiveSessionBindingsRejectsUnpinnedActiveRunner(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Runner["codex-session"] = RunnerConfig{
		Mode:    "process-once-json",
		Command: "/usr/bin/true",
	}
	cfg.Groups = []GroupConfig{
		{ID: "chat-a@g.us", Alias: "codex", Runner: "codex-session", Mode: GroupModeActiveSession, ActiveSessionID: "codex-session", Enabled: true},
	}
	err := ValidateActiveSessionBindings(cfg)
	if err == nil || !strings.Contains(err.Error(), "runner codex-session must pin CODEX_RUNNER_SESSION_ID or CLAUDE_RUNNER_SESSION_ID to codex-session") {
		t.Fatalf("error = %v, want unpinned runner guard", err)
	}
}

func TestValidateActiveSessionBindingsRejectsMismatchedActiveRunnerSession(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Runner["codex-session"] = RunnerConfig{
		Mode:    "process-once-json",
		Command: "/usr/bin/true",
		Env:     map[string]string{"CODEX_RUNNER_SESSION_ID": "other-session"},
	}
	cfg.Groups = []GroupConfig{
		{ID: "chat-a@g.us", Alias: "codex", Runner: "codex-session", Mode: GroupModeActiveSession, ActiveSessionID: "codex-session", Enabled: true},
	}
	err := ValidateActiveSessionBindings(cfg)
	if err == nil || !strings.Contains(err.Error(), "runner codex-session pins session other-session but group uses codex-session") {
		t.Fatalf("error = %v, want mismatched runner session guard", err)
	}
}

func TestValidateActiveSessionBindingsAllowsMatchingPinnedActiveRunner(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Runner["claude-session"] = RunnerConfig{
		Mode:    "process-once-json",
		Command: "/usr/bin/true",
		Env:     map[string]string{"CLAUDE_RUNNER_SESSION_ID": "claude-session"},
	}
	cfg.Groups = []GroupConfig{
		{ID: "chat-a@g.us", Alias: "claude", Runner: "claude-session", Mode: GroupModeActiveSession, ActiveSessionID: "claude-session", Enabled: true},
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

func TestSaveRejectsStaleLoadedConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	first, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	first.Active.AckMode = "verbose"
	if err := Save(path, first); err != nil {
		t.Fatal(err)
	}
	stale.Active.AckMode = "off"
	if err := Save(path, stale); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale Save error = %v, want ErrConfigChanged", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Active.AckMode != "verbose" {
		t.Fatalf("stale save replaced newer config: ack_mode=%q", loaded.Active.AckMode)
	}
}

func TestSaveRejectsConfigCreatedAfterLoadOrDefault(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	staleMissing, resolvedPath, err := LoadOrDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedPath != path {
		t.Fatalf("resolved path = %q, want %q", resolvedPath, path)
	}
	concurrent := Default()
	concurrent.Active.AckMode = "verbose"
	if err := Save(path, concurrent); err != nil {
		t.Fatal(err)
	}
	staleMissing.Active.AckMode = "off"
	if err := Save(path, staleMissing); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("missing-snapshot Save error = %v, want ErrConfigChanged", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Active.AckMode != "verbose" {
		t.Fatalf("missing snapshot replaced concurrent config: ack_mode=%q", loaded.Active.AckMode)
	}
}

func TestSaveIfMissingPreservesExistingConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	existing := Default()
	existing.Active.AckMode = "verbose"
	if err := Save(path, existing); err != nil {
		t.Fatal(err)
	}
	candidate := Default()
	candidate.Active.AckMode = "off"
	if err := SaveIfMissing(path, candidate); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Active.AckMode != "verbose" {
		t.Fatalf("SaveIfMissing replaced existing config: ack_mode=%q", loaded.Active.AckMode)
	}
}

func TestSaveNeverExposesPartialConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				readerDone <- nil
				return
			default:
			}
			if _, err := Load(path); err != nil {
				readerDone <- err
				return
			}
		}
	}()
	for i := 0; i < 50; i++ {
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			cfg.Active.AckMode = "verbose"
		} else {
			cfg.Active.AckMode = "off"
		}
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-readerDone; err != nil {
		t.Fatalf("concurrent config read observed partial write: %v", err)
	}
}
