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

type GroupEvent struct {
	ChatID               string
	SenderID             string
	LeftParticipantIDs   []string
	JoinedParticipantIDs []string
	ParticipantCount     int
	Deleted              bool
	Timestamp            time.Time
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
	Type            string `json:"type"`
	MIMEType        string `json:"mime_type,omitempty"`
	FileName        string `json:"file_name,omitempty"`
	Caption         string `json:"caption,omitempty"`
	Size            uint64 `json:"size,omitempty"`
	DurationSeconds uint32 `json:"duration_seconds,omitempty"`
	LocalPath       string `json:"local_path,omitempty"`
	DownloadError   string `json:"download_error,omitempty"`
	Transcript      string `json:"transcript,omitempty"`
	TranscriptError string `json:"transcript_error,omitempty"`
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
