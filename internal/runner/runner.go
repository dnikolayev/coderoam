package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/endurantdevs/codex-whatsapp/internal/config"
)

const ProtocolVersion = "1.0"

type ChatInfo struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Alias string `json:"alias,omitempty"`
	Name  string `json:"name,omitempty"`
}

type MessageInfo struct {
	ID              string `json:"id"`
	Timestamp       string `json:"timestamp"`
	Text            string `json:"text"`
	Trigger         string `json:"trigger,omitempty"`
	RawText         string `json:"raw_text"`
	IsReplyToBridge bool   `json:"is_reply_to_bridge"`
}

type SenderInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	IsAdmin     bool   `json:"is_admin"`
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
	ChatID   string      `json:"chat_id"`
	SenderID string      `json:"sender_id"`
	Text     string      `json:"text"`
	RawText  string      `json:"raw_text"`
	Session  SessionInfo `json:"session"`
}

type Response struct {
	Version   string         `json:"version"`
	RequestID string         `json:"request_id,omitempty"`
	Actions   []Action       `json:"actions"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Action struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Emoji string `json:"emoji,omitempty"`
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
}

func NewProcessRunner(cfg config.RunnerConfig, fallbackTimeout int) *ProcessRunner {
	return &ProcessRunner{cfg: cfg, fallbackTimeout: fallbackTimeout}
}

func (r *ProcessRunner) Invoke(ctx context.Context, req Request) (Result, error) {
	if r.cfg.Command == "" {
		return Result{}, errors.New("runner command is not configured")
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
	return nil
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
