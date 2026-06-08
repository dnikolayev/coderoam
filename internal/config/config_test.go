package config

import "testing"

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
