package whatsappweb

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/dnikolayev/coderoam/internal/types"
)

func TestSessionDSNPragmas(t *testing.T) {
	got := sessionDSN("/home/u/.coderoam/wa.db")
	want := "file:/home/u/.coderoam/wa.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(normal)"
	if got != want {
		t.Fatalf("sessionDSN = %q, want %q", got, want)
	}
}

func TestClassifyQREvent(t *testing.T) {
	pairErr := errors.New("pairing exploded")
	tests := []struct {
		name    string
		item    whatsmeow.QRChannelItem
		want    qrAction
		wantErr string
	}{
		{name: "code", item: whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "2@abc"}, want: qrShowCode},
		{name: "success", item: whatsmeow.QRChannelSuccess, want: qrSucceeded},
		{name: "timeout", item: whatsmeow.QRChannelTimeout, want: qrBatchTimedOut},
		{name: "error with cause", item: whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventError, Error: pairErr}, want: qrFailed, wantErr: "pairing exploded"},
		{name: "error without cause", item: whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventError}, want: qrFailed, wantErr: "QR login failed"},
		{name: "client outdated is informational", item: whatsmeow.QRChannelClientOutdated, want: qrLogOnly},
		{name: "scanned without multidevice is informational", item: whatsmeow.QRChannelScannedWithoutMultidevice, want: qrLogOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := classifyQREvent(tt.item)
			if got != tt.want {
				t.Fatalf("action = %d, want %d", got, tt.want)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestQRImageTarget(t *testing.T) {
	if got := qrImageTarget("/tmp/custom.png", "/data/wa.db"); got != "/tmp/custom.png" {
		t.Fatalf("explicit path = %q", got)
	}
	if got := qrImageTarget("", "/data/wa.db"); got != "/data/wa.db.qr.png" {
		t.Fatalf("default path = %q", got)
	}
}

func TestShouldDeliver(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		media []types.MediaAttachment
		want  bool
	}{
		{name: "empty", want: false},
		{name: "whitespace only", text: " \n\t ", want: false},
		{name: "text", text: "hello", want: true},
		{name: "media without text", media: []types.MediaAttachment{{Type: "image"}}, want: true},
		{name: "whitespace text with media", text: "  ", media: []types.MediaAttachment{{Type: "voice"}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldDeliver(tt.text, tt.media); got != tt.want {
				t.Fatalf("shouldDeliver(%q, %d media) = %t, want %t", tt.text, len(tt.media), got, tt.want)
			}
		})
	}
}

func TestIncomingFromEventGroupMessage(t *testing.T) {
	ts := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	media := []types.MediaAttachment{{Type: "voice", Transcript: "ship it"}}
	evt := &events.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{
				Chat:     waTypes.NewJID("120363012345678901", waTypes.GroupServer),
				Sender:   waTypes.NewJID("15551234567", waTypes.DefaultUserServer),
				IsGroup:  true,
				IsFromMe: false,
			},
			ID:        "3EB0ABCDEF",
			PushName:  "Nick",
			Timestamp: ts,
		},
	}
	got := incomingFromEvent(evt, "hello", media)
	if got.ID != "3EB0ABCDEF" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.ChatID != "120363012345678901@g.us" {
		t.Fatalf("ChatID = %q", got.ChatID)
	}
	if got.ChatType != types.ChatTypeGroup {
		t.Fatalf("ChatType = %q", got.ChatType)
	}
	if got.SenderID != "15551234567@s.whatsapp.net" {
		t.Fatalf("SenderID = %q", got.SenderID)
	}
	if got.SenderName != "Nick" {
		t.Fatalf("SenderName = %q", got.SenderName)
	}
	if got.Text != "hello" || got.RawText != "hello" {
		t.Fatalf("Text = %q, RawText = %q", got.Text, got.RawText)
	}
	if len(got.Media) != 1 || got.Media[0].Transcript != "ship it" {
		t.Fatalf("Media = %+v", got.Media)
	}
	if !got.Timestamp.Equal(ts) {
		t.Fatalf("Timestamp = %v", got.Timestamp)
	}
	if got.IsFromMe {
		t.Fatal("IsFromMe should be false")
	}
}

func TestIncomingFromEventDirectMessageFromMe(t *testing.T) {
	evt := &events.Message{
		Info: waTypes.MessageInfo{
			MessageSource: waTypes.MessageSource{
				Chat:     waTypes.NewJID("15551234567", waTypes.DefaultUserServer),
				Sender:   waTypes.NewJID("15557654321", waTypes.DefaultUserServer),
				IsGroup:  false,
				IsFromMe: true,
			},
			ID: "ID2",
		},
	}
	got := incomingFromEvent(evt, "hi", nil)
	if got.ChatType != types.ChatTypeDirect {
		t.Fatalf("ChatType = %q, want direct", got.ChatType)
	}
	if !got.IsFromMe {
		t.Fatal("IsFromMe should be true")
	}
}

func TestGroupEventFromInfo(t *testing.T) {
	gjid := waTypes.NewJID("120363000000000001", waTypes.GroupServer)
	sender := waTypes.NewJID("15551234567", waTypes.DefaultUserServer)
	ts := time.Date(2026, 6, 10, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		name       string
		evt        *events.GroupInfo
		wantOK     bool
		wantJoined []string
		wantLeft   []string
		wantDelete bool
		wantSender string
	}{
		{
			name:   "name change only is dropped",
			evt:    &events.GroupInfo{JID: gjid, Name: &waTypes.GroupName{Name: "new name"}},
			wantOK: false,
		},
		{
			name:       "join",
			evt:        &events.GroupInfo{JID: gjid, Join: []waTypes.JID{sender}, Timestamp: ts},
			wantOK:     true,
			wantJoined: []string{"15551234567@s.whatsapp.net"},
		},
		{
			name:     "leave",
			evt:      &events.GroupInfo{JID: gjid, Leave: []waTypes.JID{sender}},
			wantOK:   true,
			wantLeft: []string{"15551234567@s.whatsapp.net"},
		},
		{
			name:       "delete",
			evt:        &events.GroupInfo{JID: gjid, Delete: &waTypes.GroupDelete{Deleted: true}},
			wantOK:     true,
			wantDelete: true,
		},
		{
			name:       "sender is recorded",
			evt:        &events.GroupInfo{JID: gjid, Sender: &sender, Join: []waTypes.JID{sender}},
			wantOK:     true,
			wantJoined: []string{"15551234567@s.whatsapp.net"},
			wantSender: "15551234567@s.whatsapp.net",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := groupEventFromInfo(tt.evt)
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.ChatID != "120363000000000001@g.us" {
				t.Fatalf("ChatID = %q", got.ChatID)
			}
			if strings.Join(got.JoinedParticipantIDs, ",") != strings.Join(tt.wantJoined, ",") {
				t.Fatalf("Joined = %v, want %v", got.JoinedParticipantIDs, tt.wantJoined)
			}
			if strings.Join(got.LeftParticipantIDs, ",") != strings.Join(tt.wantLeft, ",") {
				t.Fatalf("Left = %v, want %v", got.LeftParticipantIDs, tt.wantLeft)
			}
			if got.Deleted != tt.wantDelete {
				t.Fatalf("Deleted = %t, want %t", got.Deleted, tt.wantDelete)
			}
			if got.SenderID != tt.wantSender {
				t.Fatalf("SenderID = %q, want %q", got.SenderID, tt.wantSender)
			}
			if got.ParticipantCount != 0 {
				t.Fatalf("ParticipantCount = %d, want 0 (filled by caller)", got.ParticipantCount)
			}
			if tt.evt.Timestamp != (time.Time{}) && !got.Timestamp.Equal(tt.evt.Timestamp) {
				t.Fatalf("Timestamp = %v, want %v", got.Timestamp, tt.evt.Timestamp)
			}
		})
	}
}

func TestReadReceiptTarget(t *testing.T) {
	tests := []struct {
		name       string
		msg        types.IncomingMessage
		wantChat   string
		wantSender string
		wantErr    string
	}{
		{
			name:       "group message carries original sender",
			msg:        types.IncomingMessage{ID: "MSG1", ChatID: "120363000000000001@g.us", SenderID: "15551234567@s.whatsapp.net"},
			wantChat:   "120363000000000001@g.us",
			wantSender: "15551234567@s.whatsapp.net",
		},
		{
			name:     "direct message leaves sender empty",
			msg:      types.IncomingMessage{ID: "MSG2", ChatID: "15551234567@s.whatsapp.net"},
			wantChat: "15551234567@s.whatsapp.net",
		},
		{
			name:    "missing message id",
			msg:     types.IncomingMessage{ChatID: "15551234567@s.whatsapp.net"},
			wantErr: "message id is required",
		},
		{
			name:    "invalid chat id",
			msg:     types.IncomingMessage{ID: "MSG3", ChatID: "bad.1.2@s.whatsapp.net"},
			wantErr: "unexpected number of dots",
		},
		{
			name:    "invalid sender id",
			msg:     types.IncomingMessage{ID: "MSG4", ChatID: "120363000000000001@g.us", SenderID: "bad.1.2@s.whatsapp.net"},
			wantErr: "unexpected number of dots",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat, sender, err := readReceiptTarget(tt.msg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if chat.String() != tt.wantChat {
				t.Fatalf("chat = %q, want %q", chat.String(), tt.wantChat)
			}
			if tt.wantSender == "" {
				if !sender.IsEmpty() {
					t.Fatalf("sender = %q, want empty", sender.String())
				}
				return
			}
			if sender.String() != tt.wantSender {
				t.Fatalf("sender = %q, want %q", sender.String(), tt.wantSender)
			}
		})
	}
}

func TestValidateGroupCreation(t *testing.T) {
	tests := []struct {
		name         string
		groupName    string
		participants []string
		wantJIDs     []string
		wantErr      string
	}{
		{
			name:         "phone numbers are normalized",
			groupName:    "ops bridge",
			participants: []string{"+1 (555) 123-4567", "120363000000000001@g.us"},
			wantJIDs:     []string{"15551234567@s.whatsapp.net", "120363000000000001@g.us"},
		},
		{
			name:         "empty name",
			groupName:    "  ",
			participants: []string{"+15551234567"},
			wantErr:      "group name is required",
		},
		{
			name:         "name too long",
			groupName:    strings.Repeat("x", 26),
			participants: []string{"+15551234567"},
			wantErr:      "25 characters or fewer",
		},
		{
			name:      "no participants",
			groupName: "ops",
			wantErr:   "at least one participant",
		},
		{
			name:         "invalid participant",
			groupName:    "ops",
			participants: []string{"bad.1.2@s.whatsapp.net"},
			wantErr:      `invalid participant "bad.1.2@s.whatsapp.net"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jids, err := validateGroupCreation(tt.groupName, tt.participants)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := make([]string, 0, len(jids))
			for _, jid := range jids {
				got = append(got, jid.String())
			}
			if strings.Join(got, ",") != strings.Join(tt.wantJIDs, ",") {
				t.Fatalf("jids = %v, want %v", got, tt.wantJIDs)
			}
		})
	}
}

func TestGroupJID(t *testing.T) {
	jid, err := groupJID("120363000000000001@g.us")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jid.Server != waTypes.GroupServer {
		t.Fatalf("server = %q", jid.Server)
	}
	if _, err := groupJID("15551234567@s.whatsapp.net"); err == nil || !strings.Contains(err.Error(), "not a group JID") {
		t.Fatalf("DM jid err = %v", err)
	}
	if _, err := groupJID("15551234567"); err == nil || !strings.Contains(err.Error(), "not a group JID") {
		t.Fatalf("bare phone err = %v", err)
	}
	if _, err := groupJID(""); err == nil {
		t.Fatal("empty chat id should error")
	}
}

func TestChatsFromGroupsSkipsNilEntries(t *testing.T) {
	group := &waTypes.GroupInfo{JID: waTypes.NewJID("120363000000000001", waTypes.GroupServer)}
	group.Name = "ops bridge"
	group.ParticipantCount = 4
	chats := chatsFromGroups([]*waTypes.GroupInfo{nil, group, nil})
	if len(chats) != 1 {
		t.Fatalf("chats = %d, want 1", len(chats))
	}
	want := types.Chat{
		ID:               "120363000000000001@g.us",
		Type:             types.ChatTypeGroup,
		DisplayName:      "ops bridge",
		ParticipantCount: 4,
	}
	if chats[0] != want {
		t.Fatalf("chat = %+v, want %+v", chats[0], want)
	}
}

func TestParseChatID(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "phone with punctuation", in: " +1 (555) 123-4567 ", want: "15551234567@s.whatsapp.net"},
		{name: "jid passthrough", in: "120363000000000001@g.us", want: "120363000000000001@g.us"},
		{name: "empty", in: "  ", wantErr: true},
		{name: "invalid jid", in: "bad.1.2@s.whatsapp.net", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jid, err := ParseChatID(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseChatID(%q) = %q, want error", tt.in, jid.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if jid.String() != tt.want {
				t.Fatalf("jid = %q, want %q", jid.String(), tt.want)
			}
		})
	}
}

func TestNormalizePhone(t *testing.T) {
	if got := normalizePhone(" +1 (555) 123-45.67 "); got != "15551234567" {
		t.Fatalf("normalizePhone = %q", got)
	}
	if got := normalizePhone("15551234567"); got != "15551234567" {
		t.Fatalf("normalizePhone passthrough = %q", got)
	}
}
