package config

import (
	"os"
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
	cfg := Default()
	cfg.Security.StoreSessionsEncrypted = true
	ApplyDefaults(&cfg)
	if cfg.Security.StoreSessionsEncrypted {
		t.Fatal("store_sessions_encrypted should normalize to false until encryption is implemented")
	}
}

func TestValidateRunnerAllowsProcessJSONL(t *testing.T) {
	err := ValidateRunner("default", RunnerConfig{
		Mode:    "process-jsonl",
		Command: "/usr/bin/true",
	})
	if err != nil {
		t.Fatalf("ValidateRunner returned error: %v", err)
	}
}
