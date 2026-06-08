package planned

import (
	"context"
	"fmt"

	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/types"
)

type Transport struct {
	name    string
	detail  string
	handler transport.MessageHandler
}

func New(name string) *Transport {
	return &Transport{
		name:   name,
		detail: fmt.Sprintf("%s transport adapter is planned but not implemented in this release", name),
	}
}

func (t *Transport) Login(ctx context.Context, method types.LoginMethod) error {
	return fmt.Errorf("%s transport does not support login yet; use whatsapp-web for v0.1.0", t.name)
}

func (t *Transport) Connect(ctx context.Context) error {
	return fmt.Errorf("%s transport is not implemented yet; use whatsapp-web for v0.1.0", t.name)
}

func (t *Transport) Logout(ctx context.Context) error {
	return nil
}

func (t *Transport) Status(ctx context.Context) (*types.ConnectionStatus, error) {
	return &types.ConnectionStatus{Connected: false, Account: t.name, Detail: t.detail}, nil
}

func (t *Transport) ListChats(ctx context.Context) ([]types.Chat, error) {
	return nil, fmt.Errorf("%s transport cannot list chats yet", t.name)
}

func (t *Transport) CreateGroup(ctx context.Context, name string, participants []string) (*types.Chat, error) {
	return nil, fmt.Errorf("%s transport cannot create groups yet", t.name)
}

func (t *Transport) GetGroupInviteLink(ctx context.Context, chatID string, reset bool) (string, error) {
	return "", fmt.Errorf("%s transport cannot create invite links yet", t.name)
}

func (t *Transport) Subscribe(handler transport.MessageHandler) {
	t.handler = handler
}

func (t *Transport) SubscribeGroupEvents(handler transport.GroupEventHandler) {}

func (t *Transport) SendText(ctx context.Context, chatID string, text string, opts types.SendOptions) (*types.SentMessage, error) {
	return nil, fmt.Errorf("%s transport cannot send messages yet", t.name)
}

func (t *Transport) MarkRead(ctx context.Context, msg types.IncomingMessage) error {
	return nil
}

func (t *Transport) ArchiveChat(ctx context.Context, chatID string) error {
	return nil
}

func (t *Transport) Close(ctx context.Context) error {
	return nil
}
