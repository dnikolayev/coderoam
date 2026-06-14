package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRunbookSectionFreshFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	existed, err := writeRunbookSection(path, "BODY")
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Fatal("fresh file should report existed=false")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(got)
	if !strings.Contains(content, relayRunbookMarkerStart) || !strings.Contains(content, relayRunbookMarkerEnd) {
		t.Fatalf("missing markers: %q", content)
	}
	if !strings.Contains(content, "BODY") {
		t.Fatalf("missing body: %q", content)
	}
}

func TestWriteRunbookSectionIdempotentUpdate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if _, err := writeRunbookSection(path, "V1"); err != nil {
		t.Fatal(err)
	}
	existed, err := writeRunbookSection(path, "V2")
	if err != nil {
		t.Fatal(err)
	}
	if !existed {
		t.Fatal("second write should report existed=true")
	}
	content, _ := os.ReadFile(path)
	s := string(content)
	if strings.Count(s, relayRunbookMarkerStart) != 1 {
		t.Fatalf("expected exactly one section, got: %q", s)
	}
	if strings.Contains(s, "V1") || !strings.Contains(s, "V2") {
		t.Fatalf("section not updated in place: %q", s)
	}
}

func TestWriteRunbookSectionPreservesExistingContent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# My own instructions\n\nKeep me.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRunbookSection(path, "RUNBOOK"); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	s := string(content)
	if !strings.Contains(s, "Keep me.") {
		t.Fatalf("existing content was clobbered: %q", s)
	}
	if !strings.Contains(s, "RUNBOOK") {
		t.Fatalf("runbook not appended: %q", s)
	}
	// A re-run must not duplicate the user's content or the section.
	if _, err := writeRunbookSection(path, "RUNBOOK2"); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(path)
	s = string(content)
	if strings.Count(s, "Keep me.") != 1 || strings.Count(s, relayRunbookMarkerStart) != 1 {
		t.Fatalf("re-run duplicated content: %q", s)
	}
}

func TestAgentRunbookFiles(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"claude":   {"CLAUDE.md"},
		"codex":    {"AGENTS.md"},
		"opencode": {"AGENTS.md"},
		"gemini":   {"GEMINI.md"},
		"all":      {"CLAUDE.md", "AGENTS.md", "GEMINI.md"},
		"":         {"CLAUDE.md", "AGENTS.md", "GEMINI.md"},
	}
	for agent, want := range cases {
		got, err := agentRunbookFiles(agent)
		if err != nil {
			t.Fatalf("agent %q: %v", agent, err)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("agent %q = %v, want %v", agent, got, want)
		}
	}
	if _, err := agentRunbookFiles("bogus"); err == nil {
		t.Fatal("unknown agent should error")
	}
}

func TestRelayRunbookExplainsSessionIsolation(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"Pick THIS client's session id",
		"another client's group, alias, or session id",
		"return address",
		"Every client needs its own clearly named WhatsApp group",
		"coderoam active start --name \"<Agent> Session\" --alias <session-id> --session-id <session-id> --yes",
		"coderoam inbox next --session-id <session-id>",
		"coderoam notify --chat <session-id> --important --text",
	} {
		if !strings.Contains(relayRunbook, want) {
			t.Fatalf("relay runbook missing %q", want)
		}
	}
}
