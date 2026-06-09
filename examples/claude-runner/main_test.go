package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestShouldIgnoreAnswerUsesDefaultMarker(t *testing.T) {
	t.Setenv("CLAUDE_RUNNER_IGNORE_MARKER", "")

	if !shouldIgnoreAnswer("  [[coderoam-ignore]]\n") {
		t.Fatal("expected default ignore marker to be ignored")
	}
	if shouldIgnoreAnswer("[[coderoam-ignore]] extra") {
		t.Fatal("expected non-exact marker output to be sent")
	}
}

func TestShouldIgnoreAnswerUsesCustomMarker(t *testing.T) {
	t.Setenv("CLAUDE_RUNNER_IGNORE_MARKER", "IGNORE_ME")

	if !shouldIgnoreAnswer("IGNORE_ME") {
		t.Fatal("expected custom ignore marker to be ignored")
	}
	if shouldIgnoreAnswer("[[coderoam-ignore]]") {
		t.Fatal("expected default marker not to match custom marker")
	}
}

func TestBuildPromptAddsImportantOnlyPolicy(t *testing.T) {
	t.Setenv("CLAUDE_RUNNER_SYSTEM_PROMPT", "base prompt")
	t.Setenv("CLAUDE_RUNNER_IMPORTANT_ONLY", "true")
	t.Setenv("CLAUDE_RUNNER_IGNORE_MARKER", "IGNORE_ME")

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

func TestBuildPromptIncludesSlashAuthorization(t *testing.T) {
	t.Setenv("CLAUDE_RUNNER_SYSTEM_PROMPT", "base prompt")
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
	t.Setenv("CLAUDE_RUNNER_SYSTEM_PROMPT", "base prompt")
	got := buildPrompt(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "/goal ship it",
	})
	if !strings.Contains(got, "Security: sender is NOT authorized for WhatsApp slash commands.") {
		t.Fatalf("prompt missing unauthorized slash guidance: %q", got)
	}
}

func TestBuildPromptIncludesLocalAudioAttachment(t *testing.T) {
	t.Setenv("CLAUDE_RUNNER_SYSTEM_PROMPT", "base prompt")
	t.Setenv("CLAUDE_RUNNER_IMPORTANT_ONLY", "")

	got := buildPrompt(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "[voice] mime=audio/ogg; codecs=opus seconds=5",
		Media: []mediaAttachment{{
			Type:            "voice",
			MIMEType:        "audio/ogg; codecs=opus",
			DurationSeconds: 5,
			LocalPath:       "/tmp/voice.ogg",
		}},
	})
	for _, want := range []string{"Attachments:", "local_path: /tmp/voice.ogg", "transcribe it before applying"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
}

func TestBuildPromptIncludesLocalImageAttachment(t *testing.T) {
	t.Setenv("CLAUDE_RUNNER_SYSTEM_PROMPT", "base prompt")
	t.Setenv("CLAUDE_RUNNER_IMPORTANT_ONLY", "")

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

func TestBuildPromptExplainsMissingAudioDownload(t *testing.T) {
	got := buildPrompt(request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "[voice] mime=audio/ogg; codecs=opus seconds=5",
		Media: []mediaAttachment{{
			Type:            "voice",
			MIMEType:        "audio/ogg; codecs=opus",
			DurationSeconds: 5,
		}},
	})
	if !strings.Contains(got, "audio was not downloaded") {
		t.Fatalf("prompt missing download guidance: %q", got)
	}
}

func TestBuildPromptIncludesAudioTranscript(t *testing.T) {
	t.Setenv("CLAUDE_RUNNER_AUDIO_TRANSCRIBE_COMMAND", os.Args[0]+" -test.run=TestAudioTranscriberHelper -- {path}")
	t.Setenv("CODEROAM_TEST_AUDIO_TRANSCRIBER", "1")

	req := transcribeAudioAttachments(context.Background(), request{
		SenderID: "sender@s.whatsapp.net",
		ChatID:   "group@g.us",
		Text:     "[voice] mime=audio/ogg; codecs=opus seconds=5",
		Media: []mediaAttachment{{
			Type:            "voice",
			MIMEType:        "audio/ogg; codecs=opus",
			DurationSeconds: 5,
			LocalPath:       "/tmp/voice.ogg",
		}},
	}, "CLAUDE_RUNNER")
	got := buildPrompt(req)
	for _, want := range []string{"transcript: transcribed /tmp/voice.ogg", "local_path: /tmp/voice.ogg"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "transcribe it before applying") {
		t.Fatalf("prompt still asks agent to transcribe after transcript was provided: %q", got)
	}
}

func TestAudioTranscriberHelper(t *testing.T) {
	if os.Getenv("CODEROAM_TEST_AUDIO_TRANSCRIBER") != "1" {
		return
	}
	fmt.Printf("transcribed %s\n", os.Args[len(os.Args)-1])
	os.Exit(0)
}
