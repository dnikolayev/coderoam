package fake

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/types"
)

type Transport struct {
	mu           sync.Mutex
	connected    bool
	handler      transport.MessageHandler
	groupHandler transport.GroupEventHandler
	chats        []types.Chat
	Sent         []Sent
	Read         []ReadReceipt
	Archived     []string
}

type Sent struct {
	ChatID string
	Text   string
	Opts   types.SendOptions
}

type ReadReceipt struct {
	ChatID    string
	SenderID  string
	MessageID string
}

func New(chats []types.Chat) *Transport {
	return &Transport{chats: chats}
}

func (t *Transport) Login(ctx context.Context, method types.LoginMethod) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = true
	return nil
}

func (t *Transport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = true
	return nil
}

func (t *Transport) Logout(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = false
	return nil
}

func (t *Transport) Status(ctx context.Context) (*types.ConnectionStatus, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return &types.ConnectionStatus{Connected: t.connected, Account: "fake"}, nil
}

func (t *Transport) ListChats(ctx context.Context) ([]types.Chat, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]types.Chat, len(t.chats))
	copy(out, t.chats)
	return out, nil
}

func (t *Transport) CreateGroup(ctx context.Context, name string, participants []string) (*types.Chat, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	chat := types.Chat{
		ID:               fmt.Sprintf("fake-%d@g.us", len(t.chats)+1),
		Type:             types.ChatTypeGroup,
		DisplayName:      name,
		ParticipantCount: len(participants) + 1,
	}
	t.chats = append(t.chats, chat)
	return &chat, nil
}

func (t *Transport) GetGroupInviteLink(ctx context.Context, chatID string, reset bool) (string, error) {
	return "https://chat.whatsapp.com/fake-invite", nil
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
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Sent = append(t.Sent, Sent{ChatID: chatID, Text: text, Opts: opts})
	return &types.SentMessage{
		ID:     fmt.Sprintf("fake-%d", len(t.Sent)),
		ChatID: chatID,
		SentAt: time.Now(),
	}, nil
}

func (t *Transport) SentSnapshot() []Sent {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Sent, len(t.Sent))
	copy(out, t.Sent)
	return out
}

func (t *Transport) MarkRead(ctx context.Context, msg types.IncomingMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Read = append(t.Read, ReadReceipt{
		ChatID:    msg.ChatID,
		SenderID:  msg.SenderID,
		MessageID: msg.ID,
	})
	return nil
}

func (t *Transport) ArchiveChat(ctx context.Context, chatID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Archived = append(t.Archived, chatID)
	return nil
}

func (t *Transport) Inject(ctx context.Context, msg types.IncomingMessage) {
	t.mu.Lock()
	handler := t.handler
	t.mu.Unlock()
	if handler != nil {
		handler(ctx, msg)
	}
}

func (t *Transport) InjectGroupEvent(ctx context.Context, evt types.GroupEvent) {
	t.mu.Lock()
	handler := t.groupHandler
	t.mu.Unlock()
	if handler != nil {
		handler(ctx, evt)
	}
}

func (t *Transport) Close(ctx context.Context) error {
	return nil
}
