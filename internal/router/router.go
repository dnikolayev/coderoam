package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/runner"
	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/types"
)

type Router struct {
	cfg                     config.Config
	store                   *db.Store
	transport               transport.ChatTransport
	mu                      sync.Mutex
	groupMu                 map[string]*sync.Mutex
	runnerCache             map[string]runner.Runner
	activeFallbackDelay     time.Duration
	activeFallbackLimit     int
	activeFallbackScheduled map[string]bool
}

type ProcessResult struct {
	Ignored bool
	Reason  string
	Sent    []string
}

const activeWatcherStaleAfter = 15 * time.Second
const recentOutboxEchoWindow = 6 * time.Hour
const minOutboxEchoTextLength = 80

func New(cfg config.Config, store *db.Store, chatTransport transport.ChatTransport) *Router {
	config.ApplyDefaults(&cfg)
	return &Router{
		cfg:                     cfg,
		store:                   store,
		transport:               chatTransport,
		groupMu:                 map[string]*sync.Mutex{},
		runnerCache:             map[string]runner.Runner{},
		activeFallbackDelay:     time.Duration(cfg.Active.FallbackDelaySeconds) * time.Second,
		activeFallbackLimit:     cfg.Active.FallbackBatchLimit,
		activeFallbackScheduled: map[string]bool{},
	}
}

func (r *Router) SetConfig(cfg config.Config) {
	r.mu.Lock()
	r.cfg = cfg
	cached := r.runnerCache
	r.runnerCache = map[string]runner.Runner{}
	r.mu.Unlock()
	stopRunners(context.Background(), cached)
}

func (r *Router) Stop(ctx context.Context) error {
	r.mu.Lock()
	cached := r.runnerCache
	r.runnerCache = map[string]runner.Runner{}
	r.mu.Unlock()
	return stopRunners(ctx, cached)
}

func (r *Router) Handle(ctx context.Context, msg types.IncomingMessage) ProcessResult {
	lock := r.lockForGroup(msg.ChatID)
	lock.Lock()
	defer lock.Unlock()
	result, err := r.process(ctx, msg)
	if err != nil {
		result = ProcessResult{Ignored: true, Reason: err.Error()}
	}
	result = normalizeProcessResult(result)
	r.auditRoute(ctx, msg, config.GroupConfig{}, result, nil)
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

	if err := r.store.EnsureProfile(ctx, r.cfg.App.Profile); err != nil {
		return ProcessResult{}, err
	}
	_ = r.store.ExpirePendingInteractions(ctx, r.cfg.App.Profile)
	_ = r.store.UpsertChat(ctx, r.cfg.App.Profile, types.Chat{
		ID:          msg.ChatID,
		Type:        msg.ChatType,
		DisplayName: msg.ChatName,
		Alias:       group.Alias,
		Allowed:     true,
	})
	_ = r.store.UpsertSender(ctx, r.cfg.App.Profile, msg.SenderID, msg.SenderName, false)
	if r.isRecentOutboxEcho(ctx, msg) {
		return ProcessResult{Ignored: true, Reason: "recent outbox echo ignored"}, nil
	}
	interactionTrigger := ""
	if pending, ok, err := r.store.FindPendingInteraction(ctx, r.cfg.App.Profile, msg.ChatID, msg.SenderID); err != nil {
		return ProcessResult{}, err
	} else if ok {
		selectedText, selectedIndex, valid, ambiguous := resolveInteractionReplyDetail(pending, msg.Text)
		if !valid {
			reply := invalidInteractionReply(pending)
			if len(ambiguous) > 0 {
				reply = ambiguousInteractionReply(pending, ambiguous)
			}
			if sendErr := r.sendText(ctx, msg, 0, reply); sendErr != nil {
				return ProcessResult{}, sendErr
			}
			r.markRead(ctx, msg)
			return ProcessResult{Reason: "pending interaction invalid choice", Sent: []string{reply}}, nil
		}
		if err := r.store.MarkPendingInteractionAnswered(ctx, r.cfg.App.Profile, pending.ID, selectedIndex, selectedText); err != nil {
			return ProcessResult{}, err
		}
		if msg.RawText == "" {
			msg.RawText = msg.Text
		}
		msg.Text = selectedText
		interactionTrigger = "interaction"
	}

	if group.Mode == config.GroupModeActiveSession {
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
		} else if !connected && r.activeSessionFallbackAllowed(group.Runner) {
			if r.activeFallbackDelay <= 0 {
				return r.processActiveSessionFallback(ctx, msg, group, sessionID)
			}
			scheduled := r.scheduleActiveSessionFallback(msg, group, sessionID)
			if scheduled && r.shouldSendActiveAck("fallback") {
				ack := r.activeAckText("fallback", record.ID, sessionID, group.Runner)
				if _, err := r.store.QueueActiveOutbox(ctx, r.cfg.App.Profile, msg.ChatID, ack, false); err != nil {
					return ProcessResult{}, err
				}
			}
			return ProcessResult{Reason: "active inbox fallback scheduled"}, nil
		} else if connected {
			if r.shouldSendActiveAck("live") {
				ack := r.activeAckText("live", record.ID, sessionID, group.Runner)
				if _, err := r.store.QueueActiveOutbox(ctx, r.cfg.App.Profile, msg.ChatID, ack, false); err != nil {
					return ProcessResult{}, err
				}
			}
			return ProcessResult{Reason: "active inbox queued"}, nil
		}
		if r.shouldSendActiveAck("queued") {
			ack := r.activeAckText("queued", record.ID, sessionID, group.Runner)
			if _, err := r.store.QueueActiveOutbox(ctx, r.cfg.App.Profile, msg.ChatID, ack, false); err != nil {
				return ProcessResult{}, err
			}
		}
		return ProcessResult{Reason: "active inbox queued"}, nil
	}

	text := msg.Text
	trigger := interactionTrigger
	if interactionTrigger == "" {
		var ok bool
		text, trigger, ok = r.applyTrigger(msg)
		if !ok {
			return ProcessResult{Ignored: true, Reason: "trigger not matched"}, nil
		}
	}

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
	processRunner := r.runnerFor(msg.ChatID, runnerID, runnerCfg)
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
		if action.Type == "ignore" || (action.Text == "" && !isInteractionAction(action.Type)) {
			continue
		}
		if isInteractionAction(action.Type) {
			promptAction := normalizeInteractionAction(action)
			promptText := formatInteractionPrompt(promptAction)
			if promptText == "" {
				continue
			}
			if sendErr := r.sendText(ctx, msg, messageID, promptText); sendErr != nil {
				return ProcessResult{Ignored: false, Reason: sendErr.Error(), Sent: sent}, "send_failed", nil
			}
			if _, err := r.store.CreatePendingInteraction(ctx, db.PendingInteractionRecord{
				ProfileID:       r.cfg.App.Profile,
				ChatID:          msg.ChatID,
				SenderID:        msg.SenderID,
				RunnerID:        runnerID,
				SourceMessageID: messageID,
				Prompt:          promptAction.Text,
				Options:         promptAction.Options,
				ExpiresAt:       time.Now().Add(interactionTTL(promptAction)),
			}); err != nil {
				return ProcessResult{}, "interaction_error", err
			}
			sent = append(sent, promptText)
			continue
		}
		if action.Type != "reply" && action.Type != "error" {
			continue
		}
		for _, chunk := range chunkText(action.Text, r.cfg.RateLimits.MaxResponseChars, r.cfg.Reply.MaxChunks, r.cfg.Reply.ChunkSeparator) {
			if sendErr := r.sendText(ctx, msg, messageID, chunk); sendErr != nil {
				return ProcessResult{Ignored: false, Reason: sendErr.Error(), Sent: sent}, "send_failed", nil
			}
			sent = append(sent, chunk)
		}
		if options, ok := detectReplyInteraction(action.Text); ok {
			if _, err := r.store.CreatePendingInteraction(ctx, db.PendingInteractionRecord{
				ProfileID:       r.cfg.App.Profile,
				ChatID:          msg.ChatID,
				SenderID:        msg.SenderID,
				RunnerID:        runnerID,
				SourceMessageID: messageID,
				Prompt:          action.Text,
				Options:         options,
				ExpiresAt:       time.Now().Add(15 * time.Minute),
			}); err != nil {
				return ProcessResult{}, "interaction_error", err
			}
		}
	}
	return normalizeProcessResult(ProcessResult{Sent: sent}), messageStatus, nil
}

func (r *Router) scheduleActiveSessionFallback(msg types.IncomingMessage, group config.GroupConfig, sessionID string) bool {
	delay := r.activeFallbackDelay
	if delay < 0 {
		delay = 0
	}
	key := activeFallbackScheduleKey(r.cfg.App.Profile, msg.ChatID, sessionID)
	r.mu.Lock()
	if r.activeFallbackScheduled == nil {
		r.activeFallbackScheduled = map[string]bool{}
	}
	if r.activeFallbackScheduled[key] {
		r.mu.Unlock()
		return false
	}
	r.activeFallbackScheduled[key] = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.activeFallbackScheduled, key)
			r.mu.Unlock()
		}()
		for {
			if delay > 0 {
				timer := time.NewTimer(delay)
				<-timer.C
			}
			lock := r.lockForGroup(msg.ChatID)
			lock.Lock()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			result, err := r.processActiveSessionFallback(ctx, msg, group, sessionID)
			cancel()
			lock.Unlock()
			if err != nil {
				result = ProcessResult{Ignored: true, Reason: err.Error()}
			}
			r.auditRoute(context.Background(), msg, group, result, map[string]any{"async_fallback": true})
			if err != nil || result.Ignored || result.Reason == "active inbox fallback skipped because watcher connected" {
				return
			}
		}
	}()
	return true
}

func normalizeProcessResult(result ProcessResult) ProcessResult {
	if strings.TrimSpace(result.Reason) != "" {
		return result
	}
	if result.Ignored {
		result.Reason = "ignored"
	} else if len(result.Sent) > 0 {
		result.Reason = "runner processed"
	} else {
		result.Reason = "processed without reply"
	}
	return result
}

func (r *Router) processActiveSessionFallback(ctx context.Context, msg types.IncomingMessage, group config.GroupConfig, sessionID string) (ProcessResult, error) {
	if _, connected, err := r.store.ActiveWatcherFresh(ctx, r.cfg.App.Profile, sessionID, activeWatcherStaleAfter); err != nil {
		return ProcessResult{}, err
	} else if connected {
		return ProcessResult{Reason: "active inbox fallback skipped because watcher connected"}, nil
	}
	claimed, err := r.store.ClaimActiveInboxBatchForSession(ctx, r.cfg.App.Profile, msg.ChatID, sessionID, r.activeFallbackLimit)
	if err != nil {
		return ProcessResult{}, err
	}
	if len(claimed) == 0 {
		return ProcessResult{Ignored: true, Reason: "active inbox fallback found no unread message"}, nil
	}
	fallbackMsg, text := incomingFromActiveInboxBatch(claimed, msg)
	result, _, err := r.invokeRunnerAndSend(ctx, fallbackMsg, group, group.Runner, claimed[0].ID, text, "")
	status := "done"
	if err != nil {
		status = "ignored"
	}
	for _, record := range claimed {
		_ = r.store.MarkActiveInboxDone(ctx, r.cfg.App.Profile, record.ID, status)
	}
	if err != nil {
		return ProcessResult{}, err
	}
	if len(claimed) > 1 {
		result.Reason = "active inbox fallback batch processed"
	} else {
		result.Reason = "active inbox fallback runner processed"
	}
	return result, nil
}

func activeFallbackScheduleKey(profileID, chatID, sessionID string) string {
	return strings.Join([]string{profileID, chatID, sessionID}, "\x00")
}

func (r *Router) activeAckMode() string {
	mode := strings.ToLower(strings.TrimSpace(r.cfg.Active.AckMode))
	switch mode {
	case "off", "verbose":
		return mode
	default:
		return "minimal"
	}
}

func (r *Router) shouldSendActiveAck(kind string) bool {
	switch r.activeAckMode() {
	case "off":
		return false
	case "verbose":
		return true
	default:
		return kind == "fallback"
	}
}

func (r *Router) activeAckText(kind string, recordID int64, sessionID, runnerID string) string {
	if r.activeAckMode() == "verbose" {
		switch kind {
		case "fallback":
			return fmt.Sprintf("Received #%d by bridge for session %s. Waiting briefly for related messages, then continuing through runner %s.", recordID, sessionID, runnerID)
		case "live":
			return fmt.Sprintf("Received #%d by bridge for session %s. Queued for the live Codex session.", recordID, sessionID)
		default:
			return fmt.Sprintf("Received #%d by bridge for session %s. Queued for the active Codex session to claim; it will stay pending until a live watcher or Codex drain reads it.", recordID, sessionID)
		}
	}
	return fmt.Sprintf("Working on this in session %s; grouping nearby messages briefly.", sessionID)
}

func (r *Router) activeSessionFallbackAllowed(runnerID string) bool {
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return false
	}
	cfg, ok := r.cfg.Runner[runnerID]
	if !ok {
		return false
	}
	if strings.TrimSpace(cfg.Env["CODEX_RUNNER_SESSION_ID"]) != "" {
		return false
	}
	if strings.TrimSpace(cfg.Env["CLAUDE_RUNNER_SESSION_ID"]) != "" {
		return false
	}
	return strings.TrimSpace(cfg.Command) != ""
}

func (r *Router) runnerFor(chatID, runnerID string, runnerCfg config.RunnerConfig) runner.Runner {
	if runnerCfg.Mode != "process-jsonl" {
		return runner.NewProcessRunner(runnerCfg, r.cfg.RateLimits.MaxRunnerSeconds)
	}
	key := strings.Join([]string{r.cfg.App.Profile, chatID, runnerID}, "\x00")
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runnerCache == nil {
		r.runnerCache = map[string]runner.Runner{}
	}
	if cached, ok := r.runnerCache[key]; ok {
		return cached
	}
	cached := runner.NewProcessRunner(runnerCfg, r.cfg.RateLimits.MaxRunnerSeconds)
	r.runnerCache[key] = cached
	return cached
}

func stopRunners(ctx context.Context, runners map[string]runner.Runner) error {
	var firstErr error
	for _, item := range runners {
		if err := item.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *Router) isRecentOutboxEcho(ctx context.Context, msg types.IncomingMessage) bool {
	text := strings.TrimSpace(msg.Text)
	if len(text) < minOutboxEchoTextLength {
		return false
	}
	ok, err := r.store.RecentlySentText(ctx, r.cfg.App.Profile, msg.ChatID, text, time.Now().Add(-recentOutboxEchoWindow))
	return err == nil && ok
}

func (r *Router) sendText(ctx context.Context, msg types.IncomingMessage, messageID int64, text string) error {
	if r.transport == nil {
		return nil
	}
	_, sendErr := r.transport.SendText(ctx, msg.ChatID, text, types.SendOptions{
		QuoteOriginal:     r.cfg.Reply.QuoteOriginal,
		OriginalMessageID: msg.ID,
		TypingIndicator:   r.cfg.Reply.TypingIndicator,
	})
	if sendErr != nil {
		_ = r.store.RecordOutboxFailure(ctx, r.cfg.App.Profile, msg.ChatID, messageID, text, sendErr)
		return sendErr
	}
	_ = r.store.RecordOutboxSent(ctx, r.cfg.App.Profile, msg.ChatID, messageID, text)
	return nil
}

func (r *Router) auditRoute(ctx context.Context, msg types.IncomingMessage, group config.GroupConfig, result ProcessResult, extra map[string]any) {
	if r.store == nil || msg.ChatID == "" {
		return
	}
	if group.ID == "" {
		if resolved, ok := config.FindGroup(r.cfg, msg.ChatID); ok {
			group = resolved
		}
	}
	details := map[string]any{
		"message_id":   msg.ID,
		"sender_id":    msg.SenderID,
		"reason":       result.Reason,
		"ignored":      result.Ignored,
		"sent_count":   len(result.Sent),
		"chat_type":    msg.ChatType,
		"is_from_me":   msg.IsFromMe,
		"has_media":    len(msg.Media) > 0,
		"text_preview": truncateForAudit(msg.Text, 160),
	}
	if group.ID != "" {
		details["group_alias"] = group.Alias
		details["group_mode"] = group.Mode
		details["runner"] = group.Runner
		details["active_session_id"] = config.ActiveSessionID(group)
		details["relay_managed"] = group.RelayManaged
		details["archived"] = group.Archived
	}
	for key, value := range extra {
		details[key] = value
	}
	_ = r.store.Audit(ctx, r.cfg.App.Profile, "route_decision", msg.SenderID, msg.ChatID, details)
}

func truncateForAudit(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func incomingFromActiveInbox(record db.ActiveInboxRecord, fallback types.IncomingMessage) types.IncomingMessage {
	msg := fallback
	msg.ID = record.ExternalMessageID
	msg.ChatID = record.ChatID
	msg.SenderID = record.SenderID
	msg.SenderName = record.SenderName
	msg.Text = record.Text
	msg.RawText = record.RawText
	msg.Media = record.Media
	msg.Timestamp = record.ReceivedAt
	if msg.RawText == "" {
		msg.RawText = msg.Text
	}
	return msg
}

func incomingFromActiveInboxBatch(records []db.ActiveInboxRecord, fallback types.IncomingMessage) (types.IncomingMessage, string) {
	if len(records) == 0 {
		return fallback, strings.TrimSpace(fallback.Text)
	}
	if len(records) == 1 {
		msg := incomingFromActiveInbox(records[0], fallback)
		return msg, msg.Text
	}
	msg := incomingFromActiveInbox(records[0], fallback)
	parts := []string{"Multiple related WhatsApp messages arrived close together. Treat them as one combined user turn:"}
	media := []types.MediaAttachment{}
	ids := []string{}
	for i, record := range records {
		label := fmt.Sprintf("Message %d", i+1)
		if record.SenderName != "" {
			label += " from " + record.SenderName
		}
		text := strings.TrimSpace(record.Text)
		if text == "" {
			text = strings.TrimSpace(record.RawText)
		}
		parts = append(parts, fmt.Sprintf("%s:\n%s", label, text))
		media = append(media, record.Media...)
		ids = append(ids, record.ExternalMessageID)
	}
	combined := strings.Join(parts, "\n\n")
	msg.ID = strings.Join(ids, "+")
	msg.Text = combined
	msg.RawText = combined
	msg.Media = media
	return msg, combined
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
			Media:           msg.Media,
			IsReplyToBridge: msg.IsReplyToBridge,
		},
		Sender: runner.SenderInfo{
			ID:          msg.SenderID,
			DisplayName: msg.SenderName,
			IsAdmin:     contains(r.cfg.Security.AdminSenderIDs, msg.SenderID),
			IsAllowed:   r.senderAllowed(msg.SenderID),
		},
		Context: runner.RequestContext{
			SessionID:      sessionID,
			RecentMessages: []runner.HistoryItem{},
		},
		ChatID:   msg.ChatID,
		SenderID: msg.SenderID,
		Text:     text,
		RawText:  msg.RawText,
		Media:    msg.Media,
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

func isInteractionAction(actionType string) bool {
	switch actionType {
	case "request_input", "request_choice", "request_approval":
		return true
	default:
		return false
	}
}

func normalizeInteractionAction(action runner.Action) runner.Action {
	action.Text = strings.TrimSpace(action.Text)
	action.Options = cleanOptions(action.Options)
	if action.Type == "request_approval" && len(action.Options) == 0 {
		action.Options = []string{"Approve", "Reject"}
	}
	if action.Text == "" {
		if len(action.Options) > 0 {
			action.Text = "Choose an option."
		} else {
			action.Text = "Please reply with your answer."
		}
	}
	return action
}

func formatInteractionPrompt(action runner.Action) string {
	action = normalizeInteractionAction(action)
	var b strings.Builder
	b.WriteString(action.Text)
	if len(action.Options) > 0 {
		b.WriteString("\n\nReply with one option:")
		for i, option := range action.Options {
			b.WriteString(fmt.Sprintf("\n%d. %s", i+1, option))
		}
	} else {
		b.WriteString("\n\nReply with your answer.")
	}
	return b.String()
}

func interactionTTL(action runner.Action) time.Duration {
	if action.ExpiresSeconds > 0 {
		return time.Duration(action.ExpiresSeconds) * time.Second
	}
	return 15 * time.Minute
}

func resolveInteractionReply(record db.PendingInteractionRecord, text string) (string, int, bool) {
	selectedText, selectedIndex, valid, _ := resolveInteractionReplyDetail(record, text)
	return selectedText, selectedIndex, valid
}

func resolveInteractionReplyDetail(record db.PendingInteractionRecord, text string) (string, int, bool, []int) {
	value := strings.TrimSpace(text)
	if value == "" {
		return "", 0, false, nil
	}
	if len(record.Options) == 0 {
		return value, 0, true, nil
	}
	if index, ok := parseChoiceIndex(value); ok && index >= 1 && index <= len(record.Options) {
		return record.Options[index-1], index, true, nil
	}
	for i, option := range record.Options {
		if strings.EqualFold(value, strings.TrimSpace(option)) {
			return strings.TrimSpace(option), i + 1, true, nil
		}
	}
	valueNorm := normalizeChoiceText(value)
	valueTokens := meaningfulChoiceTokens(valueNorm)
	bestIndex := -1
	bestScore := 0
	tied := []int{}
	for i, option := range record.Options {
		option = strings.TrimSpace(option)
		score := choiceMatchScore(valueNorm, valueTokens, normalizeChoiceText(option))
		if score > bestScore {
			bestScore = score
			bestIndex = i
			tied = []int{i}
		} else if score > 0 && score == bestScore {
			tied = append(tied, i)
		}
	}
	if bestIndex >= 0 && len(tied) == 1 {
		return strings.TrimSpace(record.Options[bestIndex]), bestIndex + 1, true, nil
	}
	if len(tied) > 1 {
		choices := make([]int, 0, len(tied))
		for _, index := range tied {
			choices = append(choices, index+1)
		}
		return "", 0, false, choices
	}
	return "", 0, false, nil
}

func choiceMatchScore(valueNorm string, valueTokens []string, optionNorm string) int {
	if valueNorm == "" || optionNorm == "" {
		return 0
	}
	if valueNorm == optionNorm {
		return 1000
	}
	if len(optionNorm) >= 12 && strings.Contains(valueNorm, optionNorm) {
		return 900
	}
	if len(valueNorm) >= 12 && strings.Contains(optionNorm, valueNorm) {
		return 800
	}
	optionTokens := meaningfulChoiceTokens(optionNorm)
	overlap := 0
	for _, token := range valueTokens {
		for _, optionToken := range optionTokens {
			if token == optionToken {
				overlap++
				break
			}
		}
	}
	if overlap >= 2 {
		return 100 + overlap
	}
	if overlap == 1 && len(valueTokens) == 1 {
		return 50
	}
	return 0
}

func normalizeChoiceText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastSpace := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func meaningfulChoiceTokens(value string) []string {
	stop := map[string]bool{
		"a": true, "an": true, "and": true, "for": true, "i": true, "in": true, "it": true,
		"local": true, "md": true, "next": true, "of": true, "or": true, "please": true,
		"review": true, "the": true, "this": true, "to": true, "with": true,
	}
	tokens := []string{}
	seen := map[string]bool{}
	for _, token := range strings.Fields(value) {
		if len(token) < 2 || stop[token] || seen[token] {
			continue
		}
		switch token {
		case "doc", "docs":
			token = "documentation"
		}
		seen[token] = true
		tokens = append(tokens, token)
	}
	return tokens
}

func parseChoiceIndex(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	if end < len(value) {
		next := value[end]
		if next != '.' && next != ')' && next != ' ' && next != '\t' {
			return 0, false
		}
	}
	var index int
	for _, ch := range value[:end] {
		index = index*10 + int(ch-'0')
	}
	return index, true
}

func invalidInteractionReply(record db.PendingInteractionRecord) string {
	if len(record.Options) == 0 {
		return "I did not receive an answer. Reply with the text you want to send."
	}
	var b strings.Builder
	b.WriteString("I did not recognize that choice. Reply with a number")
	if len(record.Options) > 0 {
		b.WriteString(" or words from the option, for example: ")
		b.WriteString(choiceExample(record.Options[0]))
	}
	b.WriteString(".")
	for i, option := range record.Options {
		b.WriteString(fmt.Sprintf("\n%d. %s", i+1, option))
	}
	return b.String()
}

func ambiguousInteractionReply(record db.PendingInteractionRecord, indexes []int) string {
	var b strings.Builder
	b.WriteString("That could mean more than one option. Which one?")
	for _, index := range indexes {
		if index <= 0 || index > len(record.Options) {
			continue
		}
		b.WriteString(fmt.Sprintf("\n%d. %s", index, record.Options[index-1]))
	}
	return b.String()
}

func choiceExample(option string) string {
	tokens := meaningfulChoiceTokens(normalizeChoiceText(option))
	if len(tokens) == 0 {
		return "`1`"
	}
	if len(tokens) > 2 {
		tokens = tokens[:2]
	}
	return "`" + strings.Join(tokens, " ") + "`"
}

func detectReplyInteraction(text string) ([]string, bool) {
	options := extractNumberedOptions(text)
	if len(options) > 0 {
		return options, true
	}
	if strings.Contains(text, "?") && len(strings.TrimSpace(text)) <= 4000 {
		return nil, true
	}
	return nil, false
}

func extractNumberedOptions(text string) []string {
	options := []string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		index, ok := parseChoiceIndex(line)
		if !ok || index != len(options)+1 {
			continue
		}
		rest := strings.TrimSpace(line)
		for len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			rest = rest[1:]
		}
		rest = strings.TrimLeft(rest, ".) \t")
		if rest != "" {
			options = append(options, rest)
		}
	}
	if len(options) > 12 {
		return options[:12]
	}
	return options
}

func cleanOptions(options []string) []string {
	cleaned := []string{}
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		cleaned = append(cleaned, option)
		if len(cleaned) == 12 {
			break
		}
	}
	return cleaned
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
