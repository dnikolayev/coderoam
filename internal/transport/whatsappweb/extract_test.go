package whatsappweb

import (
	"context"
	"strings"
	"testing"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/dnikolayev/coderoam/internal/types"
)

func TestExtractBaseText(t *testing.T) {
	tests := []struct {
		name    string
		message *waProto.Message
		want    string
	}{
		{name: "nil message", message: nil, want: ""},
		{name: "empty message", message: &waProto.Message{}, want: ""},
		{
			name:    "plain conversation",
			message: &waProto.Message{Conversation: proto.String("hello")},
			want:    "hello",
		},
		{
			name: "extended text (links, formatting)",
			message: &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String("see https://example.com")},
			},
			want: "see https://example.com",
		},
		{
			name: "quoted reply keeps only the reply text",
			message: &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String("@bridge yes, do that"),
					ContextInfo: &waProto.ContextInfo{
						StanzaID:      proto.String("ORIG1"),
						Participant:   proto.String("15551234567@s.whatsapp.net"),
						QuotedMessage: &waProto.Message{Conversation: proto.String("should I deploy?")},
					},
				},
			},
			want: "@bridge yes, do that",
		},
		{
			name: "conversation wins over extended text",
			message: &waProto.Message{
				Conversation:        proto.String("primary"),
				ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String("secondary")},
			},
			want: "primary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractBaseText(tt.message); got != tt.want {
				t.Fatalf("extractBaseText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractMedia(t *testing.T) {
	tests := []struct {
		name    string
		message *waProto.Message
		want    *types.MediaAttachment // nil means no media expected
	}{
		{name: "nil message", message: nil},
		{name: "text only", message: &waProto.Message{Conversation: proto.String("hi")}},
		{
			name: "document with filename and caption",
			message: &waProto.Message{
				DocumentMessage: &waProto.DocumentMessage{
					Mimetype:   proto.String("application/pdf"),
					FileName:   proto.String("runbook.pdf"),
					Caption:    proto.String("latest runbook"),
					FileLength: proto.Uint64(2048),
				},
			},
			want: &types.MediaAttachment{Type: "document", MIMEType: "application/pdf", FileName: "runbook.pdf", Caption: "latest runbook", Size: 2048},
		},
		{
			name: "video with caption and duration",
			message: &waProto.Message{
				VideoMessage: &waProto.VideoMessage{
					Mimetype:   proto.String("video/mp4"),
					Caption:    proto.String("demo"),
					FileLength: proto.Uint64(4096),
					Seconds:    proto.Uint32(12),
				},
			},
			want: &types.MediaAttachment{Type: "video", MIMEType: "video/mp4", Caption: "demo", Size: 4096, DurationSeconds: 12},
		},
		{
			name: "audio file (not push-to-talk) stays audio",
			message: &waProto.Message{
				AudioMessage: &waProto.AudioMessage{
					Mimetype:   proto.String("audio/mpeg"),
					FileLength: proto.Uint64(999),
					Seconds:    proto.Uint32(30),
					PTT:        proto.Bool(false),
				},
			},
			want: &types.MediaAttachment{Type: "audio", MIMEType: "audio/mpeg", Size: 999, DurationSeconds: 30},
		},
		{
			name: "sticker",
			message: &waProto.Message{
				StickerMessage: &waProto.StickerMessage{
					Mimetype:   proto.String("image/webp"),
					FileLength: proto.Uint64(111),
				},
			},
			want: &types.MediaAttachment{Type: "sticker", MIMEType: "image/webp", Size: 111},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media := extractMedia(tt.message)
			if tt.want == nil {
				if len(media) != 0 {
					t.Fatalf("media = %+v, want none", media)
				}
				return
			}
			if len(media) != 1 {
				t.Fatalf("media count = %d, want 1", len(media))
			}
			if media[0] != *tt.want {
				t.Fatalf("media = %+v, want %+v", media[0], *tt.want)
			}
		})
	}
}

func TestExtractTextAndMediaMediaWithoutText(t *testing.T) {
	text, media := extractForTest(t, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			Mimetype: proto.String("application/zip"),
			FileName: proto.String("logs.zip"),
		},
	})
	if len(media) != 1 {
		t.Fatalf("media count = %d, want 1", len(media))
	}
	if !strings.HasPrefix(text, "[document]") {
		t.Fatalf("text = %q, want media summary only", text)
	}
}

// Disappearing-mode chats wrap the real payload in EphemeralMessage. whatsmeow
// unwraps it via UnwrapRaw before dispatching the event, so the transport must
// extract from the unwrapped Message. This test pins that contract.
func TestExtractTextAndMediaEphemeralWrappedCaption(t *testing.T) {
	evt := &events.Message{
		RawMessage: &waProto.Message{
			EphemeralMessage: &waProto.FutureProofMessage{
				Message: &waProto.Message{
					ImageMessage: &waProto.ImageMessage{
						Caption:  proto.String("vanishes in 7 days"),
						Mimetype: proto.String("image/jpeg"),
					},
				},
			},
		},
	}
	evt.UnwrapRaw()
	if !evt.IsEphemeral {
		t.Fatal("expected IsEphemeral after UnwrapRaw")
	}
	text, media := extractForTest(t, evt.Message)
	if len(media) != 1 || media[0].Type != "image" || media[0].Caption != "vanishes in 7 days" {
		t.Fatalf("media = %+v", media)
	}
	if !strings.Contains(text, "caption=vanishes in 7 days") {
		t.Fatalf("text = %q", text)
	}
}

// View-once messages get the same upstream unwrapping treatment.
func TestExtractTextAndMediaViewOnceWrappedVoiceNote(t *testing.T) {
	evt := &events.Message{
		RawMessage: &waProto.Message{
			ViewOnceMessageV2: &waProto.FutureProofMessage{
				Message: &waProto.Message{
					AudioMessage: &waProto.AudioMessage{
						Mimetype: proto.String("audio/ogg; codecs=opus"),
						Seconds:  proto.Uint32(4),
						PTT:      proto.Bool(true),
					},
				},
			},
		},
	}
	evt.UnwrapRaw()
	if !evt.IsViewOnce {
		t.Fatal("expected IsViewOnce after UnwrapRaw")
	}
	text, media := extractForTest(t, evt.Message)
	if len(media) != 1 || media[0].Type != "voice" {
		t.Fatalf("media = %+v", media)
	}
	if !strings.Contains(text, "[voice]") {
		t.Fatalf("text = %q", text)
	}
	if !isAudioAttachment(media[0]) {
		t.Fatal("voice note should qualify for transcription")
	}
}

// Edits arrive wrapped in EditedMessage; after UnwrapRaw the transport sees
// the replacement text like any other message.
func TestExtractTextAndMediaEditedMessage(t *testing.T) {
	evt := &events.Message{
		RawMessage: &waProto.Message{
			EditedMessage: &waProto.FutureProofMessage{
				Message: &waProto.Message{
					ProtocolMessage: &waProto.ProtocolMessage{
						EditedMessage: &waProto.Message{Conversation: proto.String("fixed typo")},
					},
				},
			},
		},
	}
	evt.UnwrapRaw()
	if !evt.IsEdit {
		t.Fatal("expected IsEdit after UnwrapRaw")
	}
	// Pin current behavior: the unwrapped edit is a ProtocolMessage, which
	// extracts to nothing, so the relay drops edits instead of re-delivering.
	text, media := extractForTest(t, evt.Message)
	if shouldDeliver(text, media) {
		t.Fatalf("edited message should be dropped, got text=%q media=%+v", text, media)
	}
}

func TestCombineTextAndMedia(t *testing.T) {
	media := []types.MediaAttachment{{Type: "image", MIMEType: "image/png"}}
	tests := []struct {
		name  string
		text  string
		media []types.MediaAttachment
		want  string
	}{
		{name: "text only passes through", text: "hello", want: "hello"},
		{name: "empty text without media stays empty", want: ""},
		{name: "media without text is summary only", media: media, want: "[image] mime=image/png"},
		{name: "whitespace text with media is summary only", text: "  \n", media: media, want: "[image] mime=image/png"},
		{name: "text and media are joined", text: " deploy this ", media: media, want: "deploy this\n\n[image] mime=image/png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := combineTextAndMedia(tt.text, tt.media); got != tt.want {
				t.Fatalf("combineTextAndMedia = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatMediaSummaryFallbacksAndErrors(t *testing.T) {
	text := formatMediaSummary([]types.MediaAttachment{
		{DownloadError: "download blew up"},
		{Type: "voice", TranscriptError: "whisper failed"},
	})
	lines := strings.Split(text, "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: %q", len(lines), text)
	}
	if !strings.HasPrefix(lines[0], "[media]") || !strings.Contains(lines[0], "download_error=download blew up") {
		t.Fatalf("line 0 = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "[voice]") || !strings.Contains(lines[1], "transcript_error=whisper failed") {
		t.Fatalf("line 1 = %q", lines[1])
	}
}

func TestMediaFileName(t *testing.T) {
	name := mediaFileName("3EB0/AB#C?", 0, types.MediaAttachment{Type: "voice", MIMEType: "audio/ogg"})
	if name != "3EB0_AB_C-01-voice.ogg" {
		t.Fatalf("name = %q", name)
	}
	fallback := mediaFileName("???", 4, types.MediaAttachment{MIMEType: "application/x-unknown-thing"})
	if fallback != "message-05-media.bin" {
		t.Fatalf("fallback = %q", fallback)
	}
}

func TestMediaExtension(t *testing.T) {
	tests := []struct {
		mime string
		want string
	}{
		{mime: "audio/ogg; codecs=opus", want: ".ogg"},
		{mime: "audio/ogg", want: ".ogg"},
		{mime: "audio/opus", want: ".opus"},
		{mime: "audio/mpeg", want: ".mp3"},
		{mime: "audio/mp4", want: ".m4a"},
		{mime: "audio/aac", want: ".m4a"},
		{mime: "application/x-unknown-thing", want: ".bin"},
		{mime: "", want: ".bin"},
	}
	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			if got := mediaExtension(types.MediaAttachment{MIMEType: tt.mime}); got != tt.want {
				t.Fatalf("mediaExtension(%q) = %q, want %q", tt.mime, got, tt.want)
			}
		})
	}
}

func TestTranscriberCommand(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		path     string
		wantName string
		wantArgs []string
	}{
		{
			name:     "placeholder is replaced",
			command:  "whisper --file {path} --lang auto",
			path:     "/tmp/v.ogg",
			wantName: "whisper",
			wantArgs: []string{"--file", "/tmp/v.ogg", "--lang", "auto"},
		},
		{
			name:     "placeholder embedded in a flag",
			command:  "transcribe --input={path}",
			path:     "/tmp/v.ogg",
			wantName: "transcribe",
			wantArgs: []string{"--input=/tmp/v.ogg"},
		},
		{
			name:     "no placeholder appends the path",
			command:  "whisper-cli",
			path:     "/tmp/v.ogg",
			wantName: "whisper-cli",
			wantArgs: []string{"/tmp/v.ogg"},
		},
		{
			name:     "empty command",
			command:  "   ",
			path:     "/tmp/v.ogg",
			wantName: "",
			wantArgs: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, args := transcriberCommand(tt.command, tt.path)
			if name != tt.wantName {
				t.Fatalf("name = %q, want %q", name, tt.wantName)
			}
			if strings.Join(args, " ") != strings.Join(tt.wantArgs, " ") {
				t.Fatalf("args = %v, want %v", args, tt.wantArgs)
			}
		})
	}
}

func TestIsAudioAttachment(t *testing.T) {
	tests := []struct {
		name string
		item types.MediaAttachment
		want bool
	}{
		{name: "voice", item: types.MediaAttachment{Type: "voice"}, want: true},
		{name: "audio", item: types.MediaAttachment{Type: "audio"}, want: true},
		{name: "case and spacing", item: types.MediaAttachment{Type: " Voice "}, want: true},
		{name: "audio mime with other type", item: types.MediaAttachment{Type: "document", MIMEType: "audio/ogg"}, want: true},
		{name: "image", item: types.MediaAttachment{Type: "image", MIMEType: "image/png"}, want: false},
		{name: "empty", item: types.MediaAttachment{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAudioAttachment(tt.item); got != tt.want {
				t.Fatalf("isAudioAttachment(%+v) = %t, want %t", tt.item, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{name: "zero limit keeps value", in: "abc", limit: 0, want: "abc"},
		{name: "under limit", in: "abc", limit: 10, want: "abc"},
		{name: "over limit", in: "abcdefgh", limit: 4, want: "abcd..."},
		{name: "trims whitespace first", in: "  abc  ", limit: 10, want: "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.limit); got != tt.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
		})
	}
}

func TestDownloadableForMedia(t *testing.T) {
	message := &waProto.Message{
		ImageMessage:    &waProto.ImageMessage{Mimetype: proto.String("image/png")},
		AudioMessage:    &waProto.AudioMessage{Mimetype: proto.String("audio/ogg")},
		VideoMessage:    &waProto.VideoMessage{Mimetype: proto.String("video/mp4")},
		DocumentMessage: &waProto.DocumentMessage{Mimetype: proto.String("application/pdf")},
		StickerMessage:  &waProto.StickerMessage{Mimetype: proto.String("image/webp")},
	}
	if got := downloadableForMedia(message, "image"); got != message.GetImageMessage() {
		t.Fatalf("image = %v", got)
	}
	if got := downloadableForMedia(message, "voice"); got != message.GetAudioMessage() {
		t.Fatalf("voice = %v", got)
	}
	if got := downloadableForMedia(message, "audio"); got != message.GetAudioMessage() {
		t.Fatalf("audio = %v", got)
	}
	if got := downloadableForMedia(message, "video"); got != message.GetVideoMessage() {
		t.Fatalf("video = %v", got)
	}
	if got := downloadableForMedia(message, "document"); got != message.GetDocumentMessage() {
		t.Fatalf("document = %v", got)
	}
	if got := downloadableForMedia(message, "sticker"); got != message.GetStickerMessage() {
		t.Fatalf("sticker = %v", got)
	}
	if got := downloadableForMedia(message, "location"); got != nil {
		t.Fatalf("unknown type = %v, want nil", got)
	}
}

// handleEvent should drop protocol-only events (no text, no media) and map
// everything else through incomingFromEvent; exercise it end to end through
// the real event handler with a fake subscriber.
func TestHandleEventDeliversMappedMessage(t *testing.T) {
	tr := &Transport{}
	var got []types.IncomingMessage
	tr.Subscribe(func(_ context.Context, msg types.IncomingMessage) {
		got = append(got, msg)
	})

	// A reaction-style protocol message extracts to nothing and is dropped.
	tr.handleEvent(&events.Message{
		Info:    waTypes.MessageInfo{ID: "DROP1"},
		Message: &waProto.Message{},
	})
	// Non-message events are ignored.
	tr.handleEvent("not an event")

	tr.handleEvent(&events.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{
				Chat:    waTypes.NewJID("120363000000000001", waTypes.GroupServer),
				Sender:  waTypes.NewJID("15551234567", waTypes.DefaultUserServer),
				IsGroup: true,
			},
			ID:       "KEEP1",
			PushName: "Nick",
		},
		Message: &waProto.Message{Conversation: proto.String("@bridge status")},
	})

	if len(got) != 1 {
		t.Fatalf("delivered = %d messages, want 1", len(got))
	}
	if got[0].ID != "KEEP1" || got[0].Text != "@bridge status" || got[0].ChatType != types.ChatTypeGroup {
		t.Fatalf("message = %+v", got[0])
	}
}

func TestHandleGroupInfoEventFiltersSettingsChurn(t *testing.T) {
	tr := &Transport{}
	var got []types.GroupEvent
	tr.SubscribeGroupEvents(func(_ context.Context, evt types.GroupEvent) {
		got = append(got, evt)
	})
	gjid := waTypes.NewJID("120363000000000001", waTypes.GroupServer)

	tr.handleGroupInfoEvent(nil)
	tr.handleGroupInfoEvent(&events.GroupInfo{JID: gjid, Name: &waTypes.GroupName{Name: "renamed"}})
	tr.handleGroupInfoEvent(&events.GroupInfo{JID: gjid, Leave: []waTypes.JID{waTypes.NewJID("15551234567", waTypes.DefaultUserServer)}})

	if len(got) != 1 {
		t.Fatalf("delivered = %d events, want 1", len(got))
	}
	if got[0].ChatID != "120363000000000001@g.us" || len(got[0].LeftParticipantIDs) != 1 {
		t.Fatalf("event = %+v", got[0])
	}
}
