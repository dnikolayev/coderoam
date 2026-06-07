package whatsappweb

import (
	"strings"
	"testing"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestExtractTextAndMediaIncludesImageCaption(t *testing.T) {
	text, media := extractTextAndMedia(&waProto.Message{
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
	text, media := extractTextAndMedia(&waProto.Message{
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
