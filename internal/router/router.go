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
const recentOutboxEchoWindow = 6 * time.Hour
const minOutboxEchoTextLength = 80

func New(cfg config.Config, store *db.Store, chatTransport transport.ChatTransport) *Router {
	return &Router{
		cfg:       cfg,
		store:     store,
		transport: chatTransport,
		groupMu:   map[string]*sync.Mutex{},
	}
}

func (r *Router) SetConfig(cfg config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg
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
		selectedText, selectedIndex, valid := resolveInteractionReply(pending, msg.Text)
		if !valid {
			reply := invalidInteractionReply(pending)
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
			ack := fmt.Sprintf("Received #%d by bridge for session %s. Continuing this session through runner %s.", record.ID, sessionID, group.Runner)
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
		} else if connected {
			ack := fmt.Sprintf("Received #%d by bridge for session %s. Queued for the live Codex session.", record.ID, sessionID)
			if _, err := r.store.QueueActiveOutbox(ctx, r.cfg.App.Profile, msg.ChatID, ack, false); err != nil {
				return ProcessResult{}, err
			}
			return ProcessResult{Reason: "active inbox queued"}, nil
		}
		ack := fmt.Sprintf("Received #%d by bridge for session %s. Queued for the active Codex session to claim; it will stay pending until a live watcher or Codex drain reads it.", record.ID, sessionID)
		if _, err := r.store.QueueActiveOutbox(ctx, r.cfg.App.Profile, msg.ChatID, ack, false); err != nil {
			return ProcessResult{}, err
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
	return ProcessResult{Sent: sent}, messageStatus, nil
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
	value := strings.TrimSpace(text)
	if value == "" {
		return "", 0, false
	}
	if len(record.Options) == 0 {
		return value, 0, true
	}
	if index, ok := parseChoiceIndex(value); ok && index >= 1 && index <= len(record.Options) {
		return record.Options[index-1], index, true
	}
	for i, option := range record.Options {
		if strings.EqualFold(value, strings.TrimSpace(option)) {
			return strings.TrimSpace(option), i + 1, true
		}
	}
	return "", 0, false
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
	b.WriteString("I did not recognize that choice. Reply with a number or option text:")
	for i, option := range record.Options {
		b.WriteString(fmt.Sprintf("\n%d. %s", i+1, option))
	}
	return b.String()
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
