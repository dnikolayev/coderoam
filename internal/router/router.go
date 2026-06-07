package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/endurantdevs/codex-whatsapp/internal/config"
	"github.com/endurantdevs/codex-whatsapp/internal/db"
	"github.com/endurantdevs/codex-whatsapp/internal/runner"
	"github.com/endurantdevs/codex-whatsapp/internal/transport"
	"github.com/endurantdevs/codex-whatsapp/internal/types"
)

type Router struct {
	cfg       config.Config
	store     *db.Store
	transport transport.ChatTransport
	mu        sync.Mutex
	groupMu   map[string]*sync.Mutex
}

type ProcessResult struct {
	Ignored bool
	Reason  string
	Sent    []string
}

const activeWatcherStaleAfter = 15 * time.Second

func New(cfg config.Config, store *db.Store, chatTransport transport.ChatTransport) *Router {
	return &Router{
		cfg:       cfg,
		store:     store,
		transport: chatTransport,
		groupMu:   map[string]*sync.Mutex{},
	}
}

func (r *Router) Handle(ctx context.Context, msg types.IncomingMessage) ProcessResult {
	lock := r.lockForGroup(msg.ChatID)
	lock.Lock()
	defer lock.Unlock()
	result, err := r.process(ctx, msg)
	if err != nil {
		return ProcessResult{Ignored: true, Reason: err.Error()}
	}
	return result
}

func (r *Router) process(ctx context.Context, msg types.IncomingMessage) (ProcessResult, error) {
	if msg.ID == "" {
		msg.ID = fmt.Sprintf("%s:%s:%d", msg.ChatID, msg.SenderID, msg.Timestamp.UnixNano())
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	if msg.IsFromMe && !r.cfg.Trigger.AllowOwn {
		return ProcessResult{Ignored: true, Reason: "own message ignored"}, nil
	}
	if _, err := r.killSwitchClear(); err != nil {
		return ProcessResult{Ignored: true, Reason: err.Error()}, nil
	}

	group, allowed := config.FindGroup(r.cfg, msg.ChatID)
	if r.cfg.Security.RequireGroupAllowlist && !allowed {
		return ProcessResult{Ignored: true, Reason: "chat is not allowlisted"}, nil
	}
	if !allowed {
		group = config.GroupConfig{ID: msg.ChatID, Runner: "default", Enabled: true}
	}
	if r.cfg.Security.RequireSenderAllowlist && !r.senderAllowed(msg.SenderID) {
		return ProcessResult{Ignored: true, Reason: "sender is not allowlisted"}, nil
	}

	if group.Mode == config.GroupModeActiveSession {
		if err := r.store.EnsureProfile(ctx, r.cfg.App.Profile); err != nil {
			return ProcessResult{}, err
		}
		_ = r.store.UpsertChat(ctx, r.cfg.App.Profile, types.Chat{
			ID:          msg.ChatID,
			Type:        msg.ChatType,
			DisplayName: msg.ChatName,
			Alias:       group.Alias,
			Allowed:     true,
		})
		_ = r.store.UpsertSender(ctx, r.cfg.App.Profile, msg.SenderID, msg.SenderName, false)
		sessionID := config.ActiveSessionID(group)
		record, fresh, err := r.store.StoreActiveInboxMessage(ctx, r.cfg.App.Profile, group.Alias, sessionID, msg)
		if err != nil {
			return ProcessResult{}, err
		}
		if !fresh {
			return ProcessResult{Ignored: true, Reason: "duplicate active inbox message ignored"}, nil
		}
		if _, connected, err := r.store.ActiveWatcherFresh(ctx, r.cfg.App.Profile, sessionID, activeWatcherStaleAfter); err != nil {
			return ProcessResult{}, err
		} else if !connected && strings.TrimSpace(group.Runner) != "" {
			ack := fmt.Sprintf("Received #%d by bridge for session %s. No live watcher is connected; running fallback runner %s.", record.ID, sessionID, group.Runner)
			if _, err := r.store.QueueActiveOutbox(ctx, r.cfg.App.Profile, msg.ChatID, ack, false); err != nil {
				return ProcessResult{}, err
			}
			claimed, ok, err := r.store.ClaimNextActiveInboxForSession(ctx, r.cfg.App.Profile, sessionID)
			if err != nil {
				return ProcessResult{}, err
			}
			if !ok {
				return ProcessResult{Ignored: true, Reason: "active inbox fallback found no unread message"}, nil
			}
			fallbackMsg := incomingFromActiveInbox(claimed, msg)
			result, _, err := r.invokeRunnerAndSend(ctx, fallbackMsg, group, group.Runner, claimed.ID, claimed.Text, "")
			if err != nil {
				_ = r.store.MarkActiveInboxDone(ctx, r.cfg.App.Profile, claimed.ID, "ignored")
				return ProcessResult{}, err
			}
			_ = r.store.MarkActiveInboxDone(ctx, r.cfg.App.Profile, claimed.ID, "done")
			result.Reason = "active inbox fallback runner processed"
			return result, nil
		}
		ack := fmt.Sprintf("Received #%d by bridge for session %s. Waiting for that active Codex session to claim it; blue/read receipt appears after Codex reads it.", record.ID, sessionID)
		if _, err := r.store.QueueActiveOutbox(ctx, r.cfg.App.Profile, msg.ChatID, ack, false); err != nil {
			return ProcessResult{}, err
		}
		return ProcessResult{Reason: "active inbox queued"}, nil
	}

	text, trigger, ok := r.applyTrigger(msg)
	if !ok {
		return ProcessResult{Ignored: true, Reason: "trigger not matched"}, nil
	}

	if err := r.store.EnsureProfile(ctx, r.cfg.App.Profile); err != nil {
		return ProcessResult{}, err
	}
	_ = r.store.UpsertChat(ctx, r.cfg.App.Profile, types.Chat{
		ID:          msg.ChatID,
		Type:        msg.ChatType,
		DisplayName: msg.ChatName,
		Alias:       group.Alias,
		Allowed:     true,
	})
	_ = r.store.UpsertSender(ctx, r.cfg.App.Profile, msg.SenderID, msg.SenderName, false)

	record, fresh, err := r.store.RecordIncomingMessage(ctx, r.cfg.App.Profile, msg, r.cfg.Retention.StoreMessageText)
	if err != nil {
		return ProcessResult{}, err
	}
	if !fresh {
		if record.Status != "received" {
			return ProcessResult{Ignored: true, Reason: "duplicate message ignored"}, nil
		}
	}
	r.markRead(ctx, msg)
	claimed, err := r.store.MarkMessageProcessing(ctx, record.ID)
	if err != nil {
		return ProcessResult{}, err
	}
	if !claimed {
		return ProcessResult{Ignored: true, Reason: "message already processing"}, nil
	}

	runnerID := group.Runner
	if runnerID == "" {
		runnerID = "default"
	}
	result, messageStatus, err := r.invokeRunnerAndSend(ctx, msg, group, runnerID, record.ID, text, trigger)
	if err != nil {
		_ = r.store.MarkMessageProcessed(ctx, record.ID, messageStatus)
		return ProcessResult{}, err
	}
	_ = r.store.MarkMessageProcessed(ctx, record.ID, messageStatus)
	return result, nil
}

func (r *Router) markRead(ctx context.Context, msg types.IncomingMessage) {
	if r.transport == nil || msg.ID == "" {
		return
	}
	_ = r.transport.MarkRead(ctx, msg)
}

func (r *Router) invokeRunnerAndSend(ctx context.Context, msg types.IncomingMessage, group config.GroupConfig, runnerID string, messageID int64, text, trigger string) (ProcessResult, string, error) {
	runnerCfg, ok := r.cfg.Runner[runnerID]
	if !ok {
		return ProcessResult{}, "runner_missing", fmt.Errorf("runner %q is not configured", runnerID)
	}
	if err := config.ValidateRunner(runnerID, runnerCfg); err != nil {
		return ProcessResult{}, "runner_invalid", err
	}

	req := r.buildRequest(msg, group, runnerID, text, trigger)
	requestJSON, _ := json.Marshal(req)
	processRunner := runner.NewProcessRunner(runnerCfg, r.cfg.RateLimits.MaxRunnerSeconds)
	runResult, runErr := processRunner.Invoke(ctx, req)
	responseJSON, _ := json.Marshal(runResult.Response)
	invocationStatus := "ok"
	messageStatus := "processed"
	if runErr != nil {
		invocationStatus = "error"
		messageStatus = "runner_error"
	}
	_ = r.store.RecordInvocation(ctx, db.InvocationRecord{
		ProfileID:    r.cfg.App.Profile,
		ChatID:       msg.ChatID,
		RunnerID:     runnerID,
		MessageID:    messageID,
		RequestJSON:  requestJSON,
		ResponseJSON: responseJSON,
		ExitCode:     runResult.ExitCode,
		Duration:     runResult.Duration,
		Status:       invocationStatus,
	})

	sent := []string{}
	for _, action := range normalizeActions(runResult.Response.Actions, runErr) {
		if action.Type == "ignore" || action.Text == "" {
			continue
		}
		if action.Type != "reply" && action.Type != "error" {
			continue
		}
		for _, chunk := range chunkText(action.Text, r.cfg.RateLimits.MaxResponseChars, r.cfg.Reply.MaxChunks, r.cfg.Reply.ChunkSeparator) {
			_, sendErr := r.transport.SendText(ctx, msg.ChatID, chunk, types.SendOptions{
				QuoteOriginal:     r.cfg.Reply.QuoteOriginal,
				OriginalMessageID: msg.ID,
				TypingIndicator:   r.cfg.Reply.TypingIndicator,
			})
			if sendErr != nil {
				_ = r.store.RecordOutboxFailure(ctx, r.cfg.App.Profile, msg.ChatID, messageID, chunk, sendErr)
				return ProcessResult{Ignored: false, Reason: sendErr.Error(), Sent: sent}, "send_failed", nil
			}
			_ = r.store.RecordOutboxSent(ctx, r.cfg.App.Profile, msg.ChatID, messageID, chunk)
			sent = append(sent, chunk)
		}
	}
	return ProcessResult{Sent: sent}, messageStatus, nil
}

func incomingFromActiveInbox(record db.ActiveInboxRecord, fallback types.IncomingMessage) types.IncomingMessage {
	msg := fallback
	msg.ID = record.ExternalMessageID
	msg.ChatID = record.ChatID
	msg.SenderID = record.SenderID
	msg.SenderName = record.SenderName
	msg.Text = record.Text
	msg.RawText = record.RawText
	msg.Timestamp = record.ReceivedAt
	if msg.RawText == "" {
		msg.RawText = msg.Text
	}
	return msg
}

func (r *Router) senderAllowed(senderID string) bool {
	return contains(r.cfg.Security.AllowedSenderIDs, senderID) || contains(r.cfg.Security.AdminSenderIDs, senderID)
}

func (r *Router) applyTrigger(msg types.IncomingMessage) (text string, trigger string, ok bool) {
	raw := strings.TrimSpace(msg.RawText)
	if raw == "" {
		raw = strings.TrimSpace(msg.Text)
	}
	prefix := strings.TrimSpace(r.cfg.Trigger.Prefix)
	if r.cfg.Trigger.AlwaysOn {
		return strings.TrimSpace(msg.Text), "", true
	}
	if prefix != "" && strings.HasPrefix(raw, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(raw, prefix)), prefix, true
	}
	if r.cfg.Trigger.ReplyToBridge && msg.IsReplyToBridge {
		return strings.TrimSpace(msg.Text), "reply", true
	}
	return "", "", false
}

func (r *Router) buildRequest(msg types.IncomingMessage, group config.GroupConfig, runnerID, text, trigger string) runner.Request {
	sessionID := fmt.Sprintf("%s:%s:%s", r.cfg.App.Profile, msg.ChatID, runnerID)
	timestamp := msg.Timestamp.UTC().Format(time.RFC3339Nano)
	return runner.Request{
		Version:   runner.ProtocolVersion,
		RequestID: fmt.Sprintf("req_%s", db.TextHash(msg.ID)[:16]),
		EventType: "message",
		ProfileID: r.cfg.App.Profile,
		Chat: runner.ChatInfo{
			ID:    msg.ChatID,
			Type:  string(msg.ChatType),
			Alias: group.Alias,
			Name:  msg.ChatName,
		},
		Message: runner.MessageInfo{
			ID:              msg.ID,
			Timestamp:       timestamp,
			Text:            text,
			Trigger:         trigger,
			RawText:         msg.RawText,
			IsReplyToBridge: msg.IsReplyToBridge,
		},
		Sender: runner.SenderInfo{
			ID:          msg.SenderID,
			DisplayName: msg.SenderName,
			IsAdmin:     contains(r.cfg.Security.AdminSenderIDs, msg.SenderID),
		},
		Context: runner.RequestContext{
			SessionID:      sessionID,
			RecentMessages: []runner.HistoryItem{},
		},
		ChatID:   msg.ChatID,
		SenderID: msg.SenderID,
		Text:     text,
		RawText:  msg.RawText,
		Session: runner.SessionInfo{
			ID:      sessionID,
			History: []runner.HistoryItem{},
		},
	}
}

func (r *Router) lockForGroup(chatID string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lock, ok := r.groupMu[chatID]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	r.groupMu[chatID] = lock
	return lock
}

func (r *Router) killSwitchClear() (bool, error) {
	if _, err := os.Stat(config.KillSwitchPath()); err == nil {
		return false, fmt.Errorf("kill switch is active at %s", config.KillSwitchPath())
	} else if !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

func normalizeActions(actions []runner.Action, runErr error) []runner.Action {
	if len(actions) > 0 {
		if runErr == nil {
			return actions
		}
		return []runner.Action{{Type: "error", Text: runner.SafeErrorText(runErr)}}
	}
	if runErr != nil {
		return []runner.Action{{Type: "error", Text: runner.SafeErrorText(runErr)}}
	}
	return []runner.Action{{Type: "ignore"}}
}

func chunkText(text string, maxChars int, maxChunks int, separator string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxChars <= 0 {
		maxChars = 8000
	}
	if maxChunks <= 0 {
		maxChunks = 5
	}
	totalLimit := maxChars * maxChunks
	if len(text) > totalLimit {
		text = text[:totalLimit]
	}
	chunks := []string{}
	for len(text) > maxChars && len(chunks) < maxChunks {
		chunks = append(chunks, text[:maxChars]+separator)
		text = text[maxChars:]
	}
	if len(chunks) < maxChunks && text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
