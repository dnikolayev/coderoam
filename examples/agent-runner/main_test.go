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

func TestBuildPromptIncludesLocalImageAttachment(t *testing.T) {
	t.Setenv("AGENT_RUNNER_SYSTEM_PROMPT", "base prompt")

	got := buildPrompt(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "[image] mime=image/png caption=button is broken",
		Media: []mediaAttachment{{
			Type:      "image",
			MIMEType:  "image/png",
			Caption:   "button is broken",
			LocalPath: "/tmp/screenshot.png",
		}},
	})
	for _, want := range []string{"Attachments:", "local_path: /tmp/screenshot.png", "image/screenshot is local", "product/reference asset", "caption: button is broken"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "transcribe it before applying") {
		t.Fatalf("image prompt should not include audio transcription guidance: %q", got)
	}
}

func TestBuildPromptExplainsMissingImageDownload(t *testing.T) {
	t.Setenv("AGENT_RUNNER_SYSTEM_PROMPT", "base prompt")

	got := buildPrompt(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "[image] mime=image/jpeg",
		Media: []mediaAttachment{{
			Type:     "image",
			MIMEType: "image/jpeg",
		}},
	})
	for _, want := range []string{"image/screenshot was not downloaded", "visual content is unavailable", "enable transport.download_media"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "local_path:") {
		t.Fatalf("metadata-only image prompt should not include local_path: %q", got)
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
