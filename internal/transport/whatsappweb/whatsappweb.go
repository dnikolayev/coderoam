package whatsappweb

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal/v3"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/endurantdevs/codex-whatsapp/internal/transport"
	"github.com/endurantdevs/codex-whatsapp/internal/types"
)

type Transport struct {
	client      *whatsmeow.Client
	handler     transport.MessageHandler
	sessionPath string
	mu          sync.Mutex
}

func New(ctx context.Context, sessionPath string, logLevel string) (*Transport, error) {
	if logLevel == "" {
		logLevel = "WARN"
	}
	dbLog := waLog.Stdout("whatsmeow-db", strings.ToUpper(logLevel), false)
	clientLog := waLog.Stdout("whatsmeow", strings.ToUpper(logLevel), false)
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+sessionPath+"?_foreign_keys=on", dbLog)
	if err != nil {
		return nil, err
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, err
	}
	client := whatsmeow.NewClient(device, clientLog)
	client.EnableAutoReconnect = true
	t := &Transport{client: client, sessionPath: sessionPath}
	client.AddEventHandler(t.handleEvent)
	return t, nil
}

func (t *Transport) Login(ctx context.Context, method types.LoginMethod) error {
	if t.client.Store.ID != nil {
		return t.Connect(ctx)
	}
	if method.PairCodePhone != "" {
		if err := t.client.ConnectContext(ctx); err != nil {
			return err
		}
		code, err := t.client.PairPhone(ctx, normalizePhone(method.PairCodePhone), true, whatsmeow.PairClientMacOS, "chat-bridge")
		if err != nil {
			return err
		}
		fmt.Printf("Pairing code for %s: %s\n", method.PairCodePhone, code)
		return nil
	}
	qrChan, err := t.client.GetQRChannel(ctx)
	if err != nil {
		return err
	}
	if err := t.client.ConnectContext(ctx); err != nil {
		return err
	}
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			fmt.Println("Scan this QR code with WhatsApp: Settings -> Linked Devices -> Link a Device")
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			qrPath := method.QRImagePath
			if qrPath == "" {
				qrPath = t.sessionPath + ".qr.png"
			}
			if err := qrcode.WriteFile(evt.Code, qrcode.Medium, 512, qrPath); err == nil {
				fmt.Printf("QR image: %s\n", qrPath)
				if method.OpenQRImage {
					if err := openPath(qrPath); err != nil {
						fmt.Printf("QR image open failed: %v\n", err)
					}
				}
			} else {
				fmt.Printf("QR image write failed: %v\n", err)
			}
		case "success":
			fmt.Println("WhatsApp login succeeded.")
			return nil
		case "timeout":
			return fmt.Errorf("QR login timed out")
		case "error":
			if evt.Error != nil {
				return evt.Error
			}
			return fmt.Errorf("QR login failed")
		default:
			fmt.Printf("WhatsApp login event: %s\n", evt.Event)
		}
	}
	if t.client.IsLoggedIn() {
		return nil
	}
	return fmt.Errorf("QR login ended before account was linked")
}

func (t *Transport) Connect(ctx context.Context) error {
	if t.client.Store.ID == nil {
		return fmt.Errorf("WhatsApp is not logged in; run chat-bridge auth login --qr first")
	}
	if t.client.IsConnected() {
		return nil
	}
	return t.client.ConnectContext(ctx)
}

func (t *Transport) Logout(ctx context.Context) error {
	if t.client.IsConnected() || t.client.IsLoggedIn() {
		return t.client.Logout(ctx)
	}
	return nil
}

func (t *Transport) Status(ctx context.Context) (*types.ConnectionStatus, error) {
	account := ""
	if t.client.Store.ID != nil {
		account = t.client.Store.ID.String()
	}
	return &types.ConnectionStatus{
		Connected: t.client.IsConnected(),
		Account:   account,
		Detail:    fmt.Sprintf("logged_in=%t", t.client.IsLoggedIn()),
	}, nil
}

func (t *Transport) ListChats(ctx context.Context) ([]types.Chat, error) {
	if err := t.Connect(ctx); err != nil {
		return nil, err
	}
	groups, err := t.client.GetJoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	chats := make([]types.Chat, 0, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}
		chats = append(chats, types.Chat{
			ID:               group.JID.String(),
			Type:             types.ChatTypeGroup,
			DisplayName:      group.Name,
			ParticipantCount: group.ParticipantCount,
		})
	}
	return chats, nil
}

func (t *Transport) CreateGroup(ctx context.Context, name string, participants []string) (*types.Chat, error) {
	if err := t.Connect(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("group name is required")
	}
	if len(name) > 25 {
		return nil, fmt.Errorf("group name must be 25 characters or fewer")
	}
	if len(participants) == 0 {
		return nil, fmt.Errorf("at least one participant is required")
	}
	jids := make([]waTypes.JID, 0, len(participants))
	for _, participant := range participants {
		jid, err := ParseChatID(participant)
		if err != nil {
			return nil, fmt.Errorf("invalid participant %q: %w", participant, err)
		}
		jids = append(jids, jid)
	}
	group, err := t.client.CreateGroup(ctx, whatsmeow.ReqCreateGroup{
		Name:         name,
		Participants: jids,
		CreateKey:    t.client.GenerateMessageID(),
	})
	if err != nil {
		return nil, err
	}
	chat := &types.Chat{
		ID:               group.JID.String(),
		Type:             types.ChatTypeGroup,
		DisplayName:      group.Name,
		ParticipantCount: group.ParticipantCount,
	}
	return chat, nil
}

func (t *Transport) GetGroupInviteLink(ctx context.Context, chatID string, reset bool) (string, error) {
	if err := t.Connect(ctx); err != nil {
		return "", err
	}
	jid, err := ParseChatID(chatID)
	if err != nil {
		return "", err
	}
	if jid.Server != waTypes.GroupServer {
		return "", fmt.Errorf("chat %s is not a group JID", chatID)
	}
	return t.client.GetGroupInviteLink(ctx, jid, reset)
}

func (t *Transport) Subscribe(handler transport.MessageHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handler = handler
}

func (t *Transport) SendText(ctx context.Context, chatID string, text string, opts types.SendOptions) (*types.SentMessage, error) {
	if err := t.Connect(ctx); err != nil {
		return nil, err
	}
	jid, err := ParseChatID(chatID)
	if err != nil {
		return nil, err
	}
	if opts.TypingIndicator {
		_ = t.client.SendChatPresence(ctx, jid, waTypes.ChatPresenceComposing, waTypes.ChatPresenceMediaText)
	}
	resp, err := t.client.SendMessage(ctx, jid, &waProto.Message{Conversation: proto.String(text)})
	if opts.TypingIndicator {
		_ = t.client.SendChatPresence(ctx, jid, waTypes.ChatPresencePaused, waTypes.ChatPresenceMediaText)
	}
	if err != nil {
		return nil, err
	}
	return &types.SentMessage{ID: string(resp.ID), ChatID: jid.String(), SentAt: resp.Timestamp}, nil
}

func (t *Transport) MarkRead(ctx context.Context, msg types.IncomingMessage) error {
	if msg.ID == "" {
		return fmt.Errorf("message id is required")
	}
	if err := t.Connect(ctx); err != nil {
		return err
	}
	chat, err := ParseChatID(msg.ChatID)
	if err != nil {
		return err
	}
	sender := waTypes.JID{}
	if msg.SenderID != "" {
		sender, err = ParseChatID(msg.SenderID)
		if err != nil {
			return err
		}
	}
	return t.client.MarkRead(ctx, []waTypes.MessageID{waTypes.MessageID(msg.ID)}, time.Now(), chat, sender)
}

func (t *Transport) Close(ctx context.Context) error {
	if t.client.IsConnected() {
		t.client.Disconnect()
	}
	return nil
}

func (t *Transport) handleEvent(evt any) {
	msgEvt, ok := evt.(*events.Message)
	if !ok || msgEvt == nil || msgEvt.Message == nil {
		return
	}
	text, media := extractTextAndMedia(msgEvt.Message)
	if strings.TrimSpace(text) == "" && len(media) == 0 {
		return
	}
	t.mu.Lock()
	handler := t.handler
	t.mu.Unlock()
	if handler == nil {
		return
	}
	chatType := types.ChatTypeDirect
	if msgEvt.Info.IsGroup {
		chatType = types.ChatTypeGroup
	}
	incoming := types.IncomingMessage{
		ID:         string(msgEvt.Info.ID),
		ChatID:     msgEvt.Info.Chat.String(),
		ChatType:   chatType,
		SenderID:   msgEvt.Info.Sender.String(),
		SenderName: msgEvt.Info.PushName,
		Text:       text,
		RawText:    text,
		Media:      media,
		Timestamp:  msgEvt.Info.Timestamp,
		IsFromMe:   msgEvt.Info.IsFromMe,
	}
	handler(context.Background(), incoming)
}

func extractText(message *waProto.Message) string {
	text, _ := extractTextAndMedia(message)
	return text
}

func extractTextAndMedia(message *waProto.Message) (string, []types.MediaAttachment) {
	if message == nil {
		return "", nil
	}
	text := message.GetConversation()
	if ext := message.GetExtendedTextMessage(); ext != nil {
		if text == "" {
			text = ext.GetText()
		}
	}
	media := extractMedia(message)
	if len(media) == 0 {
		return text, nil
	}
	mediaSummary := formatMediaSummary(media)
	if strings.TrimSpace(text) == "" {
		return mediaSummary, media
	}
	return strings.TrimSpace(text) + "\n\n" + mediaSummary, media
}

func extractMedia(message *waProto.Message) []types.MediaAttachment {
	if message == nil {
		return nil
	}
	if image := message.GetImageMessage(); image != nil {
		return []types.MediaAttachment{{
			Type:     "image",
			MIMEType: image.GetMimetype(),
			Caption:  image.GetCaption(),
			Size:     image.GetFileLength(),
		}}
	}
	if video := message.GetVideoMessage(); video != nil {
		return []types.MediaAttachment{{
			Type:     "video",
			MIMEType: video.GetMimetype(),
			Caption:  video.GetCaption(),
			Size:     video.GetFileLength(),
		}}
	}
	if document := message.GetDocumentMessage(); document != nil {
		return []types.MediaAttachment{{
			Type:     "document",
			MIMEType: document.GetMimetype(),
			FileName: document.GetFileName(),
			Caption:  document.GetCaption(),
			Size:     document.GetFileLength(),
		}}
	}
	if audio := message.GetAudioMessage(); audio != nil {
		mediaType := "audio"
		if audio.GetPTT() {
			mediaType = "voice"
		}
		return []types.MediaAttachment{{
			Type:     mediaType,
			MIMEType: audio.GetMimetype(),
			Size:     audio.GetFileLength(),
		}}
	}
	if sticker := message.GetStickerMessage(); sticker != nil {
		return []types.MediaAttachment{{
			Type:     "sticker",
			MIMEType: sticker.GetMimetype(),
			Size:     sticker.GetFileLength(),
		}}
	}
	return nil
}

func formatMediaSummary(media []types.MediaAttachment) string {
	parts := make([]string, 0, len(media))
	for _, item := range media {
		label := item.Type
		if label == "" {
			label = "media"
		}
		fields := []string{"[" + label + "]"}
		if item.FileName != "" {
			fields = append(fields, "file="+item.FileName)
		}
		if item.MIMEType != "" {
			fields = append(fields, "mime="+item.MIMEType)
		}
		if item.Size > 0 {
			fields = append(fields, fmt.Sprintf("bytes=%d", item.Size))
		}
		if item.Caption != "" {
			fields = append(fields, "caption="+item.Caption)
		}
		parts = append(parts, strings.Join(fields, " "))
	}
	return strings.Join(parts, "\n")
}

func ParseChatID(value string) (waTypes.JID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return waTypes.JID{}, fmt.Errorf("chat id is required")
	}
	if strings.Contains(value, "@") {
		return waTypes.ParseJID(value)
	}
	return waTypes.NewJID(normalizePhone(value), waTypes.DefaultUserServer), nil
}

func normalizePhone(value string) string {
	replacer := strings.NewReplacer("+", "", " ", "", "-", "", "(", "", ")", "", ".", "")
	return replacer.Replace(strings.TrimSpace(value))
}

func openPath(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

func WaitForInterrupt(ctx context.Context) {
	<-ctx.Done()
	_ = time.Now()
}
