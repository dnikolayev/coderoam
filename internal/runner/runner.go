package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/types"
)

const ProtocolVersion = "1.0"

type ChatInfo struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Alias string `json:"alias,omitempty"`
	Name  string `json:"name,omitempty"`
}

type MessageInfo struct {
	ID              string                  `json:"id"`
	Timestamp       string                  `json:"timestamp"`
	Text            string                  `json:"text"`
	Trigger         string                  `json:"trigger,omitempty"`
	RawText         string                  `json:"raw_text"`
	Media           []types.MediaAttachment `json:"media,omitempty"`
	IsReplyToBridge bool                    `json:"is_reply_to_bridge"`
}

type SenderInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	IsAdmin     bool   `json:"is_admin"`
	IsAllowed   bool   `json:"is_allowed"`
}

type SessionInfo struct {
	ID      string        `json:"id"`
	History []HistoryItem `json:"history"`
}

type HistoryItem struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type RequestContext struct {
	SessionID      string        `json:"session_id"`
	RecentMessages []HistoryItem `json:"recent_messages"`
}

type Request struct {
	Version   string         `json:"version"`
	RequestID string         `json:"request_id"`
	EventType string         `json:"event_type,omitempty"`
	ProfileID string         `json:"profile_id"`
	Chat      ChatInfo       `json:"chat"`
	Message   MessageInfo    `json:"message"`
	Sender    SenderInfo     `json:"sender"`
	Context   RequestContext `json:"context,omitempty"`

	// Short fields are included for minimal runners.
	ChatID   string                  `json:"chat_id"`
	SenderID string                  `json:"sender_id"`
	Text     string                  `json:"text"`
	RawText  string                  `json:"raw_text"`
	Media    []types.MediaAttachment `json:"media,omitempty"`
	Session  SessionInfo             `json:"session"`
}

type Response struct {
	Version   string         `json:"version"`
	RequestID string         `json:"request_id,omitempty"`
	Actions   []Action       `json:"actions"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Action struct {
	Type           string   `json:"type"`
	Text           string   `json:"text,omitempty"`
	Emoji          string   `json:"emoji,omitempty"`
	Options        []string `json:"options,omitempty"`
	ExpiresSeconds int      `json:"expires_seconds,omitempty"`
}

type Result struct {
	Response Response
	ExitCode int
	Duration time.Duration
	StdErr   string
}

type Runner interface {
	Invoke(ctx context.Context, req Request) (Result, error)
	Health(ctx context.Context) error
	Stop(ctx context.Context) error
}

type ProcessRunner struct {
	cfg             config.RunnerConfig
	fallbackTimeout int
	mu              sync.Mutex
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          *bufio.Reader
	stderr          *lockedLimitedBuffer
	waitDone        chan struct{}
	starts          []time.Time
}

func NewProcessRunner(cfg config.RunnerConfig, fallbackTimeout int) *ProcessRunner {
	return &ProcessRunner{cfg: cfg, fallbackTimeout: fallbackTimeout}
}

func (r *ProcessRunner) Invoke(ctx context.Context, req Request) (Result, error) {
	if r.cfg.Command == "" {
		return Result{}, errors.New("runner command is not configured")
	}
	if r.cfg.Mode == "process-jsonl" {
		return r.invokeJSONL(ctx, req)
	}
	start := time.Now()
	timeout := config.RunnerTimeout(r.cfg, r.fallbackTimeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdin []byte
	switch r.cfg.Mode {
	case "process-once-text":
		stdin = []byte(req.Text)
	case "process-once-json", "":
		raw, err := json.Marshal(req)
		if err != nil {
			return Result{}, err
		}
		stdin = append(raw, '\n')
	default:
		return Result{}, fmt.Errorf("unsupported runner mode %q", r.cfg.Mode)
	}

	cmd := exec.CommandContext(runCtx, r.cfg.Command, r.cfg.Args...)
	cmd.Dir = r.cfg.WorkingDir
	cmd.Env = os.Environ()
	for key, value := range r.cfg.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return Result{
			Response: errorResponse(req.RequestID, "The local CLI app timed out."),
			ExitCode: exitCode,
			Duration: duration,
			StdErr:   stderr.String(),
		}, runCtx.Err()
	}
	if err != nil {
		return Result{
			Response: errorResponse(req.RequestID, "The local CLI app failed."),
			ExitCode: exitCode,
			Duration: duration,
			StdErr:   stderr.String(),
		}, err
	}

	if r.cfg.Mode == "process-once-text" {
		return Result{
			Response: Response{
				Version:   ProtocolVersion,
				RequestID: req.RequestID,
				Actions:   []Action{{Type: "reply", Text: strings.TrimSpace(stdout.String())}},
			},
			ExitCode: exitCode,
			Duration: duration,
			StdErr:   stderr.String(),
		}, nil
	}

	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return Result{
			Response: errorResponse(req.RequestID, "The local CLI app returned invalid JSON."),
			ExitCode: exitCode,
			Duration: duration,
			StdErr:   stderr.String(),
		}, err
	}
	if response.Version == "" {
		response.Version = ProtocolVersion
	}
	if response.RequestID == "" {
		response.RequestID = req.RequestID
	}
	return Result{Response: response, ExitCode: exitCode, Duration: duration, StdErr: stderr.String()}, nil
}

func (r *ProcessRunner) Health(ctx context.Context) error {
	if r.cfg.Command == "" {
		return errors.New("runner command is not configured")
	}
	return nil
}

func (r *ProcessRunner) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopJSONLLocked(ctx)
}

func (r *ProcessRunner) invokeJSONL(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	timeout := config.RunnerTimeout(r.cfg, r.fallbackTimeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	r.mu.Lock()
	defer r.mu.Unlock()

	result, err := r.invokeJSONLLocked(runCtx, req)
	result.Duration = time.Since(start)
	return result, err
}

func (r *ProcessRunner) invokeJSONLLocked(ctx context.Context, req Request) (Result, error) {
	if err := r.ensureJSONLProcessLocked(); err != nil {
		return Result{}, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return Result{}, err
	}
	payload = append(payload, '\n')
	if _, err := r.stdin.Write(payload); err != nil {
		stderrText := r.stderrText()
		_ = r.stopJSONLLocked(context.Background())
		if !r.cfg.RestartOnCrash {
			return failedJSONLResult(req.RequestID, 1, stderrText), err
		}
		if restartErr := r.ensureJSONLProcessLocked(); restartErr != nil {
			return failedJSONLResult(req.RequestID, 1, r.stderrText()), restartErr
		}
		if _, err := r.stdin.Write(payload); err != nil {
			stderrText = r.stderrText()
			_ = r.stopJSONLLocked(context.Background())
			return failedJSONLResult(req.RequestID, 1, stderrText), err
		}
	}

	type readResult struct {
		response Response
		err      error
	}
	readCh := make(chan readResult, 1)
	reader := r.stdout
	go func() {
		response, err := readJSONLResponse(reader, req.RequestID)
		readCh <- readResult{response: response, err: err}
	}()

	select {
	case <-ctx.Done():
		stderrText := r.stderrText()
		_ = r.stopJSONLLocked(context.Background())
		return Result{
			Response: errorResponse(req.RequestID, "The local CLI app timed out."),
			ExitCode: 1,
			StdErr:   stderrText,
		}, ctx.Err()
	case out := <-readCh:
		if out.err != nil {
			stderrText := r.stderrText()
			_ = r.stopJSONLLocked(context.Background())
			return failedJSONLResult(req.RequestID, 1, stderrText), out.err
		}
		if out.response.Version == "" {
			out.response.Version = ProtocolVersion
		}
		if out.response.RequestID == "" {
			out.response.RequestID = req.RequestID
		}
		return Result{
			Response: out.response,
			ExitCode: 0,
			StdErr:   r.stderrText(),
		}, nil
	}
}

func (r *ProcessRunner) ensureJSONLProcessLocked() error {
	if r.cmd != nil && r.stdin != nil && r.stdout != nil {
		return nil
	}
	if err := r.recordJSONLStartLocked(time.Now()); err != nil {
		return err
	}
	cmd := exec.Command(r.cfg.Command, r.cfg.Args...)
	cmd.Dir = r.cfg.WorkingDir
	cmd.Env = os.Environ()
	for key, value := range r.cfg.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	stderr := &lockedLimitedBuffer{max: 32 * 1024}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	waitDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stderr, stderrPipe)
	}()
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()
	r.cmd = cmd
	r.stdin = stdin
	r.stdout = bufio.NewReader(stdout)
	r.stderr = stderr
	r.waitDone = waitDone
	return nil
}

func (r *ProcessRunner) recordJSONLStartLocked(now time.Time) error {
	limit := r.cfg.MaxRestartsPerHour
	if limit <= 0 {
		return nil
	}
	cutoff := now.Add(-1 * time.Hour)
	kept := r.starts[:0]
	for _, started := range r.starts {
		if started.After(cutoff) {
			kept = append(kept, started)
		}
	}
	r.starts = kept
	if len(r.starts) >= limit {
		return fmt.Errorf("runner restart limit exceeded: max_restarts_per_hour=%d", limit)
	}
	r.starts = append(r.starts, now)
	return nil
}

func (r *ProcessRunner) stopJSONLLocked(ctx context.Context) error {
	cmd := r.cmd
	stdin := r.stdin
	waitDone := r.waitDone
	r.cmd = nil
	r.stdin = nil
	r.stdout = nil
	r.stderr = nil
	r.waitDone = nil
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// Do not inspect cmd.ProcessState here: the goroutine running Wait mutates
	// it. Kill is safe to call after process exit and avoids a race.
	_ = cmd.Process.Kill()
	if waitDone == nil {
		return nil
	}
	select {
	case <-waitDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
		return context.DeadlineExceeded
	}
}

func failedJSONLResult(requestID string, exitCode int, stderrText string) Result {
	return Result{
		Response: errorResponse(requestID, "The local CLI app failed."),
		ExitCode: exitCode,
		StdErr:   stderrText,
	}
}

func (r *ProcessRunner) stderrText() string {
	if r.stderr == nil {
		return ""
	}
	return r.stderr.String()
}

func readJSONLResponse(reader *bufio.Reader, requestID string) (Response, error) {
	partials := []string{}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			if errors.Is(err, io.EOF) {
				return Response{}, errors.New("jsonl runner exited before response")
			}
			return Response{}, err
		}
		response, done, parseErr := parseJSONLResponseLine(line, requestID, &partials)
		if parseErr != nil {
			return Response{}, parseErr
		}
		if done {
			return response, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return Response{}, errors.New("jsonl runner exited before response")
			}
			return Response{}, err
		}
	}
}

func parseJSONLResponseLine(line []byte, requestID string, partials *[]string) (Response, bool, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Response{}, false, nil
	}
	var event struct {
		Version   string         `json:"version"`
		RequestID string         `json:"request_id"`
		Type      string         `json:"type"`
		Text      string         `json:"text"`
		Actions   []Action       `json:"actions"`
		Metadata  map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return Response{}, false, err
	}
	if event.RequestID != "" && requestID != "" && event.RequestID != requestID {
		return Response{}, false, nil
	}
	if event.Version == "" {
		event.Version = ProtocolVersion
	}
	if len(event.Actions) > 0 {
		return Response{
			Version:   event.Version,
			RequestID: nonEmpty(event.RequestID, requestID),
			Actions:   event.Actions,
			Metadata:  event.Metadata,
		}, true, nil
	}
	switch event.Type {
	case "reply":
		return jsonlActionResponse(event, requestID, Action{Type: "reply", Text: event.Text}), true, nil
	case "error":
		return jsonlActionResponse(event, requestID, Action{Type: "error", Text: event.Text}), true, nil
	case "ignore":
		return jsonlActionResponse(event, requestID, Action{Type: "ignore"}), true, nil
	case "partial":
		if strings.TrimSpace(event.Text) != "" {
			*partials = append(*partials, event.Text)
		}
		return Response{}, false, nil
	case "done":
		if strings.TrimSpace(event.Text) != "" {
			*partials = append(*partials, event.Text)
		}
		text := strings.TrimSpace(strings.Join(*partials, ""))
		if text == "" {
			return jsonlActionResponse(event, requestID, Action{Type: "ignore"}), true, nil
		}
		return jsonlActionResponse(event, requestID, Action{Type: "reply", Text: text}), true, nil
	default:
		if event.Type == "" {
			return Response{}, false, errors.New("jsonl runner response missing actions or type")
		}
		return Response{}, false, fmt.Errorf("unsupported jsonl runner response type %q", event.Type)
	}
}

func jsonlActionResponse(event struct {
	Version   string         `json:"version"`
	RequestID string         `json:"request_id"`
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	Actions   []Action       `json:"actions"`
	Metadata  map[string]any `json:"metadata"`
}, fallbackRequestID string, action Action) Response {
	return Response{
		Version:   nonEmpty(event.Version, ProtocolVersion),
		RequestID: nonEmpty(event.RequestID, fallbackRequestID),
		Actions:   []Action{action},
		Metadata:  event.Metadata,
	}
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

type lockedLimitedBuffer struct {
	mu   sync.Mutex
	max  int
	data []byte
}

func (b *lockedLimitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		b.max = 32 * 1024
	}
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		b.data = b.data[len(b.data)-b.max:]
	}
	return len(p), nil
}

func (b *lockedLimitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func errorResponse(requestID, text string) Response {
	return Response{
		Version:   ProtocolVersion,
		RequestID: requestID,
		Actions:   []Action{{Type: "error", Text: text}},
	}
}

func SafeErrorText(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Bridge error: local runner timed out. Check local logs."
	}
	return "Bridge error: local runner failed. Check local logs."
}
