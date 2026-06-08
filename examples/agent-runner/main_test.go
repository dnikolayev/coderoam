package main

import (
	"strings"
	"testing"
)

func TestBuildPromptIncludesSlashAuthorization(t *testing.T) {
	t.Setenv("AGENT_RUNNER_SYSTEM_PROMPT", "base prompt")
	got := buildPrompt(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "/goal ship it",
		Sender:   senderInfo{IsAllowed: true},
	})
	if !strings.Contains(got, "Security: sender is authorized for WhatsApp slash commands.") {
		t.Fatalf("prompt missing authorized slash guidance: %q", got)
	}
}

func TestBuildPromptBlocksUnauthorizedSlashCommands(t *testing.T) {
	t.Setenv("AGENT_RUNNER_SYSTEM_PROMPT", "base prompt")
	got := buildPrompt(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "/goal ship it",
	})
	if !strings.Contains(got, "Security: sender is NOT authorized for WhatsApp slash commands.") {
		t.Fatalf("prompt missing unauthorized slash guidance: %q", got)
	}
}

func TestBuildInvocationAppendsPromptAsArgument(t *testing.T) {
	t.Setenv("AGENT_RUNNER_COMMAND", "agent")
	t.Setenv("AGENT_RUNNER_ARGS_JSON", `["run","--model","test"]`)
	t.Setenv("AGENT_RUNNER_PROMPT_MODE", "arg")
	t.Setenv("AGENT_RUNNER_SYSTEM_PROMPT", "base prompt")
	inv, err := buildInvocation(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Command != "agent" {
		t.Fatalf("command = %q, want agent", inv.Command)
	}
	if len(inv.Args) != 4 || inv.Args[0] != "run" || inv.Args[3] == "" {
		t.Fatalf("args = %#v, want static args plus prompt", inv.Args)
	}
	if inv.Stdin != "" {
		t.Fatalf("stdin = %q, want empty", inv.Stdin)
	}
}

func TestBuildInvocationCanSendPromptOnStdin(t *testing.T) {
	t.Setenv("AGENT_RUNNER_COMMAND", "agent")
	t.Setenv("AGENT_RUNNER_ARGS", "run")
	t.Setenv("AGENT_RUNNER_PROMPT_MODE", "stdin")
	t.Setenv("AGENT_RUNNER_SYSTEM_PROMPT", "base prompt")
	inv, err := buildInvocation(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Args) != 1 || inv.Args[0] != "run" {
		t.Fatalf("args = %#v, want static args only", inv.Args)
	}
	if !strings.Contains(inv.Stdin, "Message:\nhello") {
		t.Fatalf("stdin missing prompt: %q", inv.Stdin)
	}
}

func TestShouldIgnoreAnswerUsesConfiguredMarker(t *testing.T) {
	t.Setenv("AGENT_RUNNER_IGNORE_MARKER", "IGNORE_ME")
	if !shouldIgnoreAnswer(" IGNORE_ME\n") {
		t.Fatal("expected configured marker to be ignored")
	}
	if shouldIgnoreAnswer("[[coderoam-ignore]]") {
		t.Fatal("expected default marker not to match configured marker")
	}
}
