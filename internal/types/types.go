package types

import "time"

type ChatType string

const (
	ChatTypeGroup  ChatType = "group"
	ChatTypeDirect ChatType = "direct"
)

type Chat struct {
	ID               string
	Type             ChatType
	DisplayName      string
	Alias            string
	ParticipantCount int
	LastMessageAt    *time.Time
	Allowed          bool
}

type IncomingMessage struct {
	ID              string
	ChatID          string
	ChatType        ChatType
	ChatName        string
	SenderID        string
	SenderName      string
	Text            string
	RawText         string
	Media           []MediaAttachment
	Timestamp       time.Time
	IsFromMe        bool
	IsReplyToBridge bool
}

type MediaAttachment struct {
	Type     string
	MIMEType string
	FileName string
	Caption  string
	Size     uint64
}

type SendOptions struct {
	QuoteOriginal     bool
	OriginalMessageID string
	TypingIndicator   bool
}

type SentMessage struct {
	ID     string
	ChatID string
	SentAt time.Time
}

type ConnectionStatus struct {
	Connected bool
	Account   string
	Detail    string
}

type LoginMethod struct {
	QR            bool
	PairCodePhone string
	QRImagePath   string
	OpenQRImage   bool
}
