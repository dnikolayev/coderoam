package whatsappweb

// Pure decision logic extracted from the transport plumbing so it can be unit
// tested without a live whatsmeow client. Everything in this file takes plain
// inputs (proto structs, event structs, strings) and performs no I/O.

import (
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/dnikolayev/coderoam/internal/types"
)

// sessionDSN builds the modernc.org/sqlite DSN for the whatsmeow session
// store. modernc.org/sqlite (pure Go, the same driver internal/db uses)
// registers itself as "sqlite"; whatsmeow's dbutil maps any "sqlite*" dialect
// to SQLite. Pragmas must use modernc's _pragma=name(value) form - mattn-style
// params like _foreign_keys=on would be silently ignored. whatsmeow refuses
// to start without foreign keys; busy_timeout(5000) preserves the 5s default
// the previous CGO driver applied to every connection; synchronous(normal)
// preserves the synchronous=NORMAL the previous mattn driver defaulted to
// (mattn sqlite3.go sets synchronousMode := "NORMAL"), since modernc would
// otherwise leave SQLite's FULL default in place.
func sessionDSN(sessionPath string) string {
	return "file:" + sessionPath + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(normal)"
}

// qrAction is the QR login loop's next step for a QR channel item.
type qrAction int

const (
	// qrLogOnly is an informational event; print it and keep waiting.
	qrLogOnly qrAction = iota
	// qrShowCode renders a fresh QR code for the user to scan.
	qrShowCode
	// qrSucceeded means the device was linked; finish the login.
	qrSucceeded
	// qrBatchTimedOut means whatsmeow's code batch expired; the caller may
	// reconnect for a fresh batch if the login window has time left.
	qrBatchTimedOut
	// qrFailed aborts the login with the returned error.
	qrFailed
)

// classifyQREvent maps a whatsmeow QR channel item to the login loop's next
// action. Only qrFailed carries an error; an "error" item without an embedded
// error still fails with a generic message. Unknown events (e.g.
// err-client-outdated) are surfaced as qrLogOnly so the loop keeps waiting
// for the channel to close, matching whatsmeow's documented behavior of
// always ending the channel with a terminal item.
func classifyQREvent(evt whatsmeow.QRChannelItem) (qrAction, error) {
	switch evt.Event {
	case whatsmeow.QRChannelEventCode:
		return qrShowCode, nil
	case whatsmeow.QRChannelSuccess.Event:
		return qrSucceeded, nil
	case whatsmeow.QRChannelTimeout.Event:
		return qrBatchTimedOut, nil
	case whatsmeow.QRChannelEventError:
		if evt.Error != nil {
			return qrFailed, evt.Error
		}
		return qrFailed, fmt.Errorf("QR login failed")
	default:
		return qrLogOnly, nil
	}
}

// qrImageTarget picks where the QR PNG is written: the explicitly requested
// path when set, otherwise a sibling of the session store.
func qrImageTarget(requestedPath, sessionPath string) string {
	if requestedPath != "" {
		return requestedPath
	}
	return sessionPath + ".qr.png"
}

// shouldDeliver reports whether an extracted message has any content worth
// surfacing to the relay. Protocol-only messages (read receipts, reactions,
// app-state syncs, ...) extract to neither text nor media and are dropped.
func shouldDeliver(text string, media []types.MediaAttachment) bool {
	return strings.TrimSpace(text) != "" || len(media) > 0
}

// incomingFromEvent maps a whatsmeow message event - already unwrapped from
// ephemeral/view-once/edit wrappers by whatsmeow's UnwrapRaw before dispatch -
// plus the extracted text and media to the transport-neutral incoming message.
func incomingFromEvent(msgEvt *events.Message, text string, media []types.MediaAttachment) types.IncomingMessage {
	chatType := types.ChatTypeDirect
	if msgEvt.Info.IsGroup {
		chatType = types.ChatTypeGroup
	}
	return types.IncomingMessage{
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
}

// groupEventFromInfo maps a whatsmeow group-info event to the
// transport-neutral group event. It returns ok=false for changes the relay
// does not surface (name/topic/settings churn with no membership change and
// no deletion). ParticipantCount is left at zero; the caller fills it in when
// a live client is available.
func groupEventFromInfo(evt *events.GroupInfo) (types.GroupEvent, bool) {
	left := make([]string, 0, len(evt.Leave))
	for _, jid := range evt.Leave {
		left = append(left, jid.String())
	}
	joined := make([]string, 0, len(evt.Join))
	for _, jid := range evt.Join {
		joined = append(joined, jid.String())
	}
	if len(left) == 0 && len(joined) == 0 && evt.Delete == nil {
		return types.GroupEvent{}, false
	}
	groupEvent := types.GroupEvent{
		ChatID:               evt.JID.String(),
		LeftParticipantIDs:   left,
		JoinedParticipantIDs: joined,
		Deleted:              evt.Delete != nil,
		Timestamp:            evt.Timestamp,
	}
	if evt.Sender != nil {
		groupEvent.SenderID = evt.Sender.String()
	}
	return groupEvent, true
}

// readReceiptTarget resolves the chat and (optional) sender JIDs a read
// receipt is addressed to. Group receipts carry the original sender; direct
// chats leave the sender empty so whatsmeow derives it from the chat.
func readReceiptTarget(msg types.IncomingMessage) (chat waTypes.JID, sender waTypes.JID, err error) {
	if msg.ID == "" {
		return waTypes.JID{}, waTypes.JID{}, fmt.Errorf("message id is required")
	}
	chat, err = ParseChatID(msg.ChatID)
	if err != nil {
		return waTypes.JID{}, waTypes.JID{}, err
	}
	if msg.SenderID != "" {
		sender, err = ParseChatID(msg.SenderID)
		if err != nil {
			return waTypes.JID{}, waTypes.JID{}, err
		}
	}
	return chat, sender, nil
}

// validateGroupCreation enforces WhatsApp's group constraints (name required
// and at most 25 characters - the client-side subject limit - plus at least
// one participant) and resolves participant phone numbers or JIDs.
func validateGroupCreation(name string, participants []string) ([]waTypes.JID, error) {
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
	return jids, nil
}

// groupJID parses a chat id and requires it to address a group.
func groupJID(chatID string) (waTypes.JID, error) {
	jid, err := ParseChatID(chatID)
	if err != nil {
		return waTypes.JID{}, err
	}
	if jid.Server != waTypes.GroupServer {
		return waTypes.JID{}, fmt.Errorf("chat %s is not a group JID", chatID)
	}
	return jid, nil
}

// chatFromGroup maps whatsmeow group metadata to the transport-neutral chat.
func chatFromGroup(group *waTypes.GroupInfo) types.Chat {
	return types.Chat{
		ID:               group.JID.String(),
		Type:             types.ChatTypeGroup,
		DisplayName:      group.Name,
		ParticipantCount: group.ParticipantCount,
	}
}

// chatsFromGroups maps the joined-groups listing, skipping nil entries.
func chatsFromGroups(groups []*waTypes.GroupInfo) []types.Chat {
	chats := make([]types.Chat, 0, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}
		chats = append(chats, chatFromGroup(group))
	}
	return chats
}
