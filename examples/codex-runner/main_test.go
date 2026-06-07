package main

import (
	"strings"
	"testing"
)

func TestShouldIgnoreAnswerUsesDefaultMarker(t *testing.T) {
	t.Setenv("CODEX_RUNNER_IGNORE_MARKER", "")

	if !shouldIgnoreAnswer("  [[chat-bridge-ignore]]\n") {
		t.Fatal("expected default ignore marker to be ignored")
	}
	if shouldIgnoreAnswer("[[chat-bridge-ignore]] extra") {
		t.Fatal("expected non-exact marker output to be sent")
	}
}

func TestShouldIgnoreAnswerUsesCustomMarker(t *testing.T) {
	t.Setenv("CODEX_RUNNER_IGNORE_MARKER", "IGNORE_ME")

	if !shouldIgnoreAnswer("IGNORE_ME") {
		t.Fatal("expected custom ignore marker to be ignored")
	}
	if shouldIgnoreAnswer("[[chat-bridge-ignore]]") {
		t.Fatal("expected default marker not to match custom marker")
	}
}

func TestBuildPromptAddsImportantOnlyPolicy(t *testing.T) {
	t.Setenv("CODEX_RUNNER_SYSTEM_PROMPT", "base prompt")
	t.Setenv("CODEX_RUNNER_IMPORTANT_ONLY", "true")
	t.Setenv("CODEX_RUNNER_IGNORE_MARKER", "IGNORE_ME")

	got := buildPrompt(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "hello",
	})
	if !strings.Contains(got, "WhatsApp notification policy") {
		t.Fatalf("prompt missing notification policy: %q", got)
	}
	if !strings.Contains(got, "reply exactly IGNORE_ME") {
		t.Fatalf("prompt missing ignore marker: %q", got)
	}
}
