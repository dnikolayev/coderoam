package whatsappweb

import (
	"context"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/types"
)

type Transport struct {
	client                 *whatsmeow.Client
	handler                transport.MessageHandler
	groupHandler           transport.GroupEventHandler
	sessionPath            string
	downloadMedia          bool
	mediaDir               string
	transcribeAudio        bool
	audioTranscribeCommand string
	audioTranscribeTimeout time.Duration
	mu                     sync.Mutex
}

type Options struct {
	DownloadMedia                 bool
	MediaDir                      string
	TranscribeAudio               bool
	AudioTranscribeCommand        string
	AudioTranscribeTimeoutSeconds int
}

func New(ctx context.Context, sessionPath string, logLevel string) (*Transport, error) {
	return NewWithOptions(ctx, sessionPath, logLevel, Options{})
}

func NewWithOptions(ctx context.Context, sessionPath string, logLevel string, opts Options) (*Transport, error) {
	if logLevel == "" {
		logLevel = "WARN"
	}
	dbLog := waLog.Stdout("whatsmeow-db", strings.ToUpper(logLevel), false)
	clientLog := waLog.Stdout("whatsmeow", strings.ToUpper(logLevel), false)
	// modernc.org/sqlite (pure Go, the same driver internal/db uses) registers
	// itself as "sqlite"; whatsmeow's dbutil maps any "sqlite*" dialect to
	// SQLite. Pragmas must use modernc's _pragma=name(value) form - mattn-style
	// params like _foreign_keys=on would be silently ignored. whatsmeow refuses
	// to start without foreign keys, and busy_timeout(5000) preserves the 5s
	// default the previous CGO driver applied to every connection.
	container, err := sqlstore.New(ctx, "sqlite", "file:"+sessionPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", dbLog)
	if err != nil {
		return nil, err
	}
	// The session store holds linked-device credentials (full account access).
	// Keep it owner-only; encrypted-at-rest storage is still a TODO (SECURITY.md).
	if info, statErr := os.Stat(sessionPath); statErr == nil && info.Mode().Perm() != 0o600 {
		_ = os.Chmod(sessionPath, 0o600)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, err
	}
	client := whatsmeow.NewClient(device, clientLog)
	client.EnableAutoReconnect = true
	if opts.MediaDir == "" {
		opts.MediaDir = filepath.Join(os.TempDir(), "coderoam-media")
	}
	transcribeTimeout := time.Duration(opts.AudioTranscribeTimeoutSeconds) * time.Second
	if transcribeTimeout <= 0 {
		transcribeTimeout = 120 * time.Second
	}
	t := &Transport{
		client:                 client,
		sessionPath:            sessionPath,
		downloadMedia:          opts.DownloadMedia,
		mediaDir:               opts.MediaDir,
		transcribeAudio:        opts.TranscribeAudio,
		audioTranscribeCommand: opts.AudioTranscribeCommand,
		audioTranscribeTimeout: transcribeTimeout,
	}
	client.AddEventHandler(t.handleEvent)
	return t, nil
}

// qrLoginWindow is how long QR login stays scannable, re-requesting fresh
// codes as WhatsApp rotates them every ~20s, before giving up.
const qrLoginWindow = 5 * time.Minute

// postPairSettle is how long auth login keeps the new connection open after a
// successful pair so whatsmeow can finish the prekey upload and initial history
// sync that the phone's "Logging in…" screen waits on. Exiting immediately can
// leave the device half-registered and get it logged out by the server.
const postPairSettle = 20 * time.Second

func (t *Transport) Login(ctx context.Context, method types.LoginMethod) error {
	if t.client.Store.ID != nil {
		return t.Connect(ctx)
	}
	if method.PairCodePhone != "" {
		if err := t.client.ConnectContext(ctx); err != nil {
			return err
		}
		code, err := t.client.PairPhone(ctx, normalizePhone(method.PairCodePhone), true, whatsmeow.PairClientMacOS, "coderoam")
		if err != nil {
			return err
		}
		fmt.Printf("Pairing code for %s: %s\n", method.PairCodePhone, code)
		return nil
	}
	// Keep the QR login scannable for a window (default 5 minutes) instead of
	// giving up after whatsmeow's short code batch (~2 min), which leaves the
	// phone with a dead endpoint ("check your connection") if the scan lands
	// late. WhatsApp still rotates each individual code every ~20s server-side,
	// so on a batch timeout we re-request a fresh batch until the window is up.
	// The image viewer is opened only once to avoid a modal popup per rotation.
	qrWindow := qrLoginWindow
	deadline := time.Now().Add(qrWindow)
	openedViewer := false
	for {
		qrChan, err := t.client.GetQRChannel(ctx)
		if err != nil {
			return err
		}
		if err := t.client.ConnectContext(ctx); err != nil {
			return err
		}
		timedOut := false
		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("Scan this QR code with WhatsApp: Settings -> Linked Devices -> Link a Device")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				qrPath := method.QRImagePath
				if qrPath == "" {
					qrPath = t.sessionPath + ".qr.png"
				}
				if err := qrcode.WriteFile(evt.Code, qrcode.Medium, 512, qrPath); err == nil {
					fmt.Printf("QR image: %s\n", qrPath)
					if method.OpenQRImage && !openedViewer {
						if err := openPath(qrPath); err != nil {
							fmt.Printf("QR image open failed: %v\n", err)
						}
						openedViewer = true
					}
				} else {
					fmt.Printf("QR image write failed: %v\n", err)
				}
				continue
			}
			switch evt.Event {
			case "success":
				fmt.Println("WhatsApp login succeeded.")
				t.settleAfterPairing(ctx)
				return nil
			case "timeout":
				timedOut = true
			case "error":
				if evt.Error != nil {
					return evt.Error
				}
				return fmt.Errorf("QR login failed")
			default:
				fmt.Printf("WhatsApp login event: %s\n", evt.Event)
			}
			if timedOut {
				break
			}
		}
		if !timedOut {
			if t.client.IsLoggedIn() {
				return nil
			}
			return fmt.Errorf("QR login ended before account was linked")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("QR login timed out after %s without a scan", qrWindow)
		}
		fmt.Printf("QR code batch expired; fetching a fresh code (about %s left to scan)\n", time.Until(deadline).Round(time.Second))
		t.client.Disconnect()
	}
}

// settleAfterPairing holds the freshly linked connection open briefly so
// whatsmeow can finish the post-pair prekey upload and initial history sync the
// phone's "Logging in…" screen waits on, then returns. This makes a standalone
// `auth login` link the device reliably without needing `run` to be started
// immediately afterward.
func (t *Transport) settleAfterPairing(ctx context.Context) {
	fmt.Printf("Finalizing device link; holding the connection ~%s to finish syncing...\n", postPairSettle)
	select {
	case <-ctx.Done():
	case <-time.After(postPairSettle):
	}
	fmt.Println("Device link finalized.")
}

func (t *Transport) Connect(ctx context.Context) error {
	if t.client.Store.ID == nil {
		return fmt.Errorf("WhatsApp is not logged in; run coderoam auth login --qr first")
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

func (t *Transport) SubscribeGroupEvents(handler transport.GroupEventHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.groupHandler = handler
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

func (t *Transport) ArchiveChat(ctx context.Context, chatID string) error {
	if err := t.Connect(ctx); err != nil {
		return err
	}
	jid, err := ParseChatID(chatID)
	if err != nil {
		return err
	}
	errs := []string{}
	if jid.Server == waTypes.GroupServer {
		if err := t.client.LeaveGroup(ctx, jid); err != nil {
			errs = append(errs, "leave: "+err.Error())
		}
	}
	if err := t.client.SendAppState(ctx, appstate.BuildArchive(jid, true, time.Time{}, nil)); err != nil {
		errs = append(errs, "archive: "+err.Error())
	}
	if t.client.Store != nil && t.client.Store.ChatSettings != nil {
		if err := t.client.Store.ChatSettings.PutArchived(ctx, jid, true); err != nil {
			errs = append(errs, "cache archive: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("archive chat %s: %s", chatID, strings.Join(errs, "; "))
	}
	return nil
}

func (t *Transport) Close(ctx context.Context) error {
	if t.client.IsConnected() {
		t.client.Disconnect()
	}
	return nil
}

func (t *Transport) handleEvent(evt any) {
	if groupEvt, ok := evt.(*events.GroupInfo); ok {
		t.handleGroupInfoEvent(groupEvt)
		return
	}
	msgEvt, ok := evt.(*events.Message)
	if !ok || msgEvt == nil || msgEvt.Message == nil {
		return
	}
	text, media := t.extractTextAndMedia(context.Background(), msgEvt.Message, string(msgEvt.Info.ID))
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

func (t *Transport) handleGroupInfoEvent(evt *events.GroupInfo) {
	if evt == nil {
		return
	}
	t.mu.Lock()
	handler := t.groupHandler
	t.mu.Unlock()
	if handler == nil {
		return
	}
	left := make([]string, 0, len(evt.Leave))
	for _, jid := range evt.Leave {
		left = append(left, jid.String())
	}
	joined := make([]string, 0, len(evt.Join))
	for _, jid := range evt.Join {
		joined = append(joined, jid.String())
	}
	if len(left) == 0 && len(joined) == 0 && evt.Delete == nil {
		return
	}
	participantCount := 0
	if t.client != nil {
		if info, err := t.client.GetGroupInfo(context.Background(), evt.JID); err == nil && info != nil {
			participantCount = len(info.Participants)
		}
	}
	groupEvent := types.GroupEvent{
		ChatID:               evt.JID.String(),
		LeftParticipantIDs:   left,
		JoinedParticipantIDs: joined,
		ParticipantCount:     participantCount,
		Deleted:              evt.Delete != nil,
		Timestamp:            evt.Timestamp,
	}
	if evt.Sender != nil {
		groupEvent.SenderID = evt.Sender.String()
	}
	handler(context.Background(), groupEvent)
}

func extractTextAndMedia(message *waProto.Message) (string, []types.MediaAttachment) {
	if message == nil {
		return "", nil
	}
	text := extractBaseText(message)
	media := extractMedia(message)
	return combineTextAndMedia(text, media), media
}

func (t *Transport) extractTextAndMedia(ctx context.Context, message *waProto.Message, messageID string) (string, []types.MediaAttachment) {
	if message == nil {
		return "", nil
	}
	text := extractBaseText(message)
	media := extractMedia(message)
	if t.downloadMedia && len(media) > 0 {
		media = t.downloadAttachments(ctx, message, media, messageID)
	}
	if t.transcribeAudio && len(media) > 0 {
		media = transcribeAudioAttachments(ctx, media, t.audioTranscribeCommand, t.audioTranscribeTimeout)
	}
	return combineTextAndMedia(text, media), media
}

func extractBaseText(message *waProto.Message) string {
	if message == nil {
		return ""
	}
	text := message.GetConversation()
	if ext := message.GetExtendedTextMessage(); ext != nil && text == "" {
		text = ext.GetText()
	}
	return text
}

func combineTextAndMedia(text string, media []types.MediaAttachment) string {
	if len(media) == 0 {
		return text
	}
	mediaSummary := formatMediaSummary(media)
	if strings.TrimSpace(text) == "" {
		return mediaSummary
	}
	return strings.TrimSpace(text) + "\n\n" + mediaSummary
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
			Type:            "video",
			MIMEType:        video.GetMimetype(),
			Caption:         video.GetCaption(),
			Size:            video.GetFileLength(),
			DurationSeconds: video.GetSeconds(),
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
			Type:            mediaType,
			MIMEType:        audio.GetMimetype(),
			Size:            audio.GetFileLength(),
			DurationSeconds: audio.GetSeconds(),
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

func (t *Transport) downloadAttachments(ctx context.Context, message *waProto.Message, media []types.MediaAttachment, messageID string) []types.MediaAttachment {
	if t.client == nil || message == nil || t.mediaDir == "" {
		return media
	}
	out := make([]types.MediaAttachment, len(media))
	copy(out, media)
	for i := range out {
		downloadable := downloadableForMedia(message, out[i].Type)
		if downloadable == nil {
			continue
		}
		data, err := t.client.Download(ctx, downloadable)
		if err != nil {
			out[i].DownloadError = err.Error()
			continue
		}
		if err := os.MkdirAll(t.mediaDir, 0o700); err != nil {
			out[i].DownloadError = err.Error()
			continue
		}
		localPath := filepath.Join(t.mediaDir, mediaFileName(messageID, i, out[i]))
		if err := os.WriteFile(localPath, data, 0o600); err != nil {
			out[i].DownloadError = err.Error()
			continue
		}
		out[i].LocalPath = localPath
		if out[i].Size == 0 {
			out[i].Size = uint64(len(data))
		}
	}
	return out
}

func transcribeAudioAttachments(ctx context.Context, media []types.MediaAttachment, command string, timeout time.Duration) []types.MediaAttachment {
	command = strings.TrimSpace(command)
	if command == "" {
		return media
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	out := make([]types.MediaAttachment, len(media))
	copy(out, media)
	for i := range out {
		if !isAudioAttachment(out[i]) || strings.TrimSpace(out[i].LocalPath) == "" || out[i].Transcript != "" {
			continue
		}
		transcript, err := runAudioTranscriber(ctx, command, out[i].LocalPath, timeout)
		if err != nil {
			out[i].TranscriptError = truncate(err.Error(), 500)
			continue
		}
		out[i].Transcript = strings.TrimSpace(transcript)
	}
	return out
}

func runAudioTranscriber(ctx context.Context, command string, localPath string, timeout time.Duration) (string, error) {
	name, args := transcriberCommand(command, localPath)
	if name == "" {
		return "", fmt.Errorf("audio transcriber command is empty")
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, name, args...)
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("audio transcription timed out")
		}
		return "", fmt.Errorf("audio transcription failed: %w: %s", err, truncate(stderr.String(), 500))
	}
	return stdout.String(), nil
}

func transcriberCommand(command string, localPath string) (string, []string) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", nil
	}
	replaced := false
	for i, part := range parts {
		if strings.Contains(part, "{path}") {
			parts[i] = strings.ReplaceAll(part, "{path}", localPath)
			replaced = true
		}
	}
	if !replaced {
		parts = append(parts, localPath)
	}
	return parts[0], parts[1:]
}

func isAudioAttachment(item types.MediaAttachment) bool {
	kind := strings.ToLower(strings.TrimSpace(item.Type))
	mimeType := strings.ToLower(strings.TrimSpace(item.MIMEType))
	return kind == "audio" || kind == "voice" || strings.HasPrefix(mimeType, "audio/")
}

func downloadableForMedia(message *waProto.Message, mediaType string) whatsmeow.DownloadableMessage {
	switch mediaType {
	case "image":
		return message.GetImageMessage()
	case "video":
		return message.GetVideoMessage()
	case "document":
		return message.GetDocumentMessage()
	case "voice", "audio":
		return message.GetAudioMessage()
	case "sticker":
		return message.GetStickerMessage()
	default:
		return nil
	}
}

func mediaFileName(messageID string, index int, item types.MediaAttachment) string {
	stem := cleanFilePart(messageID)
	if stem == "" {
		stem = "message"
	}
	kind := cleanFilePart(item.Type)
	if kind == "" {
		kind = "media"
	}
	return fmt.Sprintf("%s-%02d-%s%s", stem, index+1, kind, mediaExtension(item))
}

func mediaExtension(item types.MediaAttachment) string {
	mediaType, _, err := mime.ParseMediaType(item.MIMEType)
	if err != nil || mediaType == "" {
		mediaType = strings.TrimSpace(item.MIMEType)
	}
	switch mediaType {
	case "audio/ogg":
		return ".ogg"
	case "audio/opus":
		return ".opus"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/aac":
		return ".m4a"
	}
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

func cleanFilePart(value string) string {
	var b strings.Builder
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "._-")
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
		if item.DurationSeconds > 0 {
			fields = append(fields, fmt.Sprintf("seconds=%d", item.DurationSeconds))
		}
		if item.LocalPath != "" {
			fields = append(fields, "local_path="+item.LocalPath)
		}
		if item.DownloadError != "" {
			fields = append(fields, "download_error="+item.DownloadError)
		}
		if item.Transcript != "" {
			fields = append(fields, "transcript="+item.Transcript)
		}
		if item.TranscriptError != "" {
			fields = append(fields, "transcript_error="+item.TranscriptError)
		}
		if item.Caption != "" {
			fields = append(fields, "caption="+item.Caption)
		}
		parts = append(parts, strings.Join(fields, " "))
	}
	return strings.Join(parts, "\n")
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
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
