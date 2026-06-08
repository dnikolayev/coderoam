package main

import (
	"context"
	"fmt"
	"os"
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

func TestBuildPromptIncludesLocalAudioAttachment(t *testing.T) {
	t.Setenv("CODEX_RUNNER_SYSTEM_PROMPT", "base prompt")
	t.Setenv("CODEX_RUNNER_IMPORTANT_ONLY", "")

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
	t.Setenv("CODEX_RUNNER_AUDIO_TRANSCRIBE_COMMAND", os.Args[0]+" -test.run=TestAudioTranscriberHelper -- {path}")
	t.Setenv("CHAT_BRIDGE_TEST_AUDIO_TRANSCRIBER", "1")

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
	}, "CODEX_RUNNER")
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

func TestBuildCodexArgsUsesApprovalPolicyConfig(t *testing.T) {
	t.Setenv("CODEX_RUNNER_APPROVAL_POLICY", "never")
	args := buildCodexArgs("/workspace", "workspace-write", "/tmp/out.txt", "", "")
	args = appendEnvArgs(args)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c approval_policy=\"never\"") {
		t.Fatalf("args missing approval policy config: %v", args)
	}
}

func TestAppendWorkspaceWriteAddDirsIncludesBridgeDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_RUNNER_CHAT_BRIDGE_DATA_DIR", dir)

	args := appendWorkspaceWriteAddDirs(
		buildCodexArgs("/workspace", "workspace-write", "/tmp/out.txt", "", ""),
		"workspace-write",
		"",
		"",
	)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--add-dir "+dir) {
		t.Fatalf("args missing chat-bridge add-dir: %v", args)
	}
}

func TestAppendWorkspaceWriteAddDirsSkipsResumeRuns(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_RUNNER_CHAT_BRIDGE_DATA_DIR", dir)

	args := appendWorkspaceWriteAddDirs(
		buildCodexArgs("/workspace", "workspace-write", "/tmp/out.txt", "last", ""),
		"workspace-write",
		"last",
		"",
	)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--add-dir") {
		t.Fatalf("resume args should not include add-dir: %v", args)
	}
}

func TestAppendWorkspaceWriteAddDirsCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_RUNNER_CHAT_BRIDGE_DATA_DIR", dir)
	t.Setenv("CODEX_RUNNER_AUTO_ADD_CHAT_BRIDGE_DIR", "false")

	args := appendWorkspaceWriteAddDirs(
		buildCodexArgs("/workspace", "workspace-write", "/tmp/out.txt", "", ""),
		"workspace-write",
		"",
		"",
	)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--add-dir") {
		t.Fatalf("disabled add-dir should not be present: %v", args)
	}
}

func TestAudioTranscriberHelper(t *testing.T) {
	if os.Getenv("CHAT_BRIDGE_TEST_AUDIO_TRANSCRIBER") != "1" {
		return
	}
	fmt.Printf("transcribed %s\n", os.Args[len(os.Args)-1])
	os.Exit(0)
}
