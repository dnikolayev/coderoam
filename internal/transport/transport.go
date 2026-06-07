package transport

import (
	"context"

	"github.com/endurantdevs/codex-whatsapp/internal/types"
)

type MessageHandler func(context.Context, types.IncomingMessage)

type ChatTransport interface {
	Login(ctx context.Context, method types.LoginMethod) error
	Connect(ctx context.Context) error
	Logout(ctx context.Context) error
	Status(ctx context.Context) (*types.ConnectionStatus, error)
	ListChats(ctx context.Context) ([]types.Chat, error)
	CreateGroup(ctx context.Context, name string, participants []string) (*types.Chat, error)
	GetGroupInviteLink(ctx context.Context, chatID string, reset bool) (string, error)
	Subscribe(handler MessageHandler)
	SendText(ctx context.Context, chatID string, text string, opts types.SendOptions) (*types.SentMessage, error)
	MarkRead(ctx context.Context, msg types.IncomingMessage) error
	Close(ctx context.Context) error
}
