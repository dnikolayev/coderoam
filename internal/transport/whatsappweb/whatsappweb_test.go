package whatsappweb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/dnikolayev/coderoam/internal/types"
)

// extractForTest runs the real extraction pipeline with downloads and
// transcription disabled, which never touches the whatsmeow client.
func extractForTest(t *testing.T, message *waProto.Message) (string, []types.MediaAttachment) {
	t.Helper()
	return (&Transport{}).extractTextAndMedia(context.Background(), message, "test-message-id")
}

func TestExtractTextAndMediaIncludesImageCaption(t *testing.T) {
	text, media := extractForTest(t, &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			Caption:    proto.String("look at this"),
			Mimetype:   proto.String("image/jpeg"),
			FileLength: proto.Uint64(12345),
		},
	})
	if len(media) != 1 {
		t.Fatalf("media count = %d, want 1", len(media))
	}
	if media[0].Type != "image" || media[0].Caption != "look at this" {
		t.Fatalf("media = %+v", media[0])
	}
	for _, want := range []string{"[image]", "mime=image/jpeg", "bytes=12345", "caption=look at this"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text %q does not contain %q", text, want)
		}
	}
}

func TestExtractTextAndMediaAppendsMediaSummaryToCaptionText(t *testing.T) {
	text, media := extractForTest(t, &waProto.Message{
		Conversation: proto.String("@bridge inspect"),
		ImageMessage: &waProto.ImageMessage{
			Mimetype:   proto.String("image/png"),
			FileLength: proto.Uint64(99),
		},
	})
	if len(media) != 1 {
		t.Fatalf("media count = %d, want 1", len(media))
	}
	if !strings.HasPrefix(text, "@bridge inspect") {
		t.Fatalf("text = %q", text)
	}
	if !strings.Contains(text, "[image]") {
		t.Fatalf("text %q does not contain media summary", text)
	}
}

func TestExtractTextAndMediaDetectsVoiceMessage(t *testing.T) {
	text, media := extractForTest(t, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			Mimetype:   proto.String("audio/ogg; codecs=opus"),
			FileLength: proto.Uint64(54321),
			Seconds:    proto.Uint32(7),
			PTT:        proto.Bool(true),
		},
	})
	if len(media) != 1 {
		t.Fatalf("media count = %d, want 1", len(media))
	}
	if media[0].Type != "voice" || media[0].MIMEType != "audio/ogg; codecs=opus" || media[0].DurationSeconds != 7 {
		t.Fatalf("media = %+v", media[0])
	}
	for _, want := range []string{"[voice]", "mime=audio/ogg; codecs=opus", "bytes=54321", "seconds=7"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text %q does not contain %q", text, want)
		}
	}
}

func TestTranscribeAudioAttachmentsAddsTranscript(t *testing.T) {
	if os.Getenv("CODEROAM_TEST_AUDIO_TRANSCRIBER") == "1" {
		fmt.Printf("transcribed %s", os.Args[len(os.Args)-1])
		os.Exit(0)
	}
	t.Setenv("CODEROAM_TEST_AUDIO_TRANSCRIBER", "1")
	media := transcribeAudioAttachments(t.Context(), []types.MediaAttachment{{
		Type:      "voice",
		MIMEType:  "audio/ogg; codecs=opus",
		LocalPath: "/tmp/voice note.ogg",
	}}, os.Args[0]+" -test.run=TestTranscribeAudioAttachmentsAddsTranscript -- {path}", 10*time.Second)
	if len(media) != 1 {
		t.Fatalf("media count = %d, want 1", len(media))
	}
	if media[0].Transcript != "transcribed /tmp/voice note.ogg" || media[0].TranscriptError != "" {
		t.Fatalf("media = %+v", media[0])
	}
}

func TestTranscribeAudioAttachmentsRecordsError(t *testing.T) {
	media := transcribeAudioAttachments(context.Background(), []types.MediaAttachment{{
		Type:      "voice",
		MIMEType:  "audio/ogg",
		LocalPath: "/tmp/missing.ogg",
	}}, "/definitely/missing/transcriber {path}", time.Second)
	if len(media) != 1 {
		t.Fatalf("media count = %d, want 1", len(media))
	}
	if media[0].TranscriptError == "" {
		t.Fatalf("expected transcript error: %+v", media[0])
	}
}

func TestFormatMediaSummaryIncludesTranscript(t *testing.T) {
	text := formatMediaSummary([]types.MediaAttachment{{
		Type:       "voice",
		MIMEType:   "audio/ogg",
		LocalPath:  "/tmp/voice.ogg",
		Transcript: "ship it",
	}})
	for _, want := range []string{"[voice]", "local_path=/tmp/voice.ogg", "transcript=ship it"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary %q does not contain %q", text, want)
		}
	}
}
