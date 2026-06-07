package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type request struct {
	Version   string `json:"version"`
	RequestID string `json:"request_id"`
	ProfileID string `json:"profile_id"`
	ChatID    string `json:"chat_id"`
	SenderID  string `json:"sender_id"`
	Text      string `json:"text"`
	RawText   string `json:"raw_text"`
}

type response struct {
	Version   string         `json:"version"`
	RequestID string         `json:"request_id,omitempty"`
	Actions   []action       `json:"actions"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type action struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		writeResponse(response{
			Version:   "1.0",
			RequestID: req.RequestID,
			Actions:   []action{{Type: "error", Text: "Invalid bridge request."}},
		})
		os.Exit(1)
	}
	answer, duration, err := runClaude(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		writeResponse(response{
			Version:   "1.0",
			RequestID: req.RequestID,
			Actions:   []action{{Type: "error", Text: "Claude runner failed. Check local logs."}},
			Metadata:  map[string]any{"runtime_ms": duration.Milliseconds()},
		})
		os.Exit(1)
	}
	if shouldIgnoreAnswer(answer) {
		writeResponse(response{
			Version:   "1.0",
			RequestID: req.RequestID,
			Actions:   []action{{Type: "ignore"}},
			Metadata:  map[string]any{"runtime_ms": duration.Milliseconds()},
		})
		return
	}
	writeResponse(response{
		Version:   "1.0",
		RequestID: req.RequestID,
		Actions:   []action{{Type: "reply", Text: strings.TrimSpace(answer)}},
		Metadata:  map[string]any{"runtime_ms": duration.Milliseconds()},
	})
}

func runClaude(req request) (string, time.Duration, error) {
	start := time.Now()
	timeout := envDuration("CLAUDE_RUNNER_TIMEOUT_SECONDS", 600*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	claudeBin := envOrDefault("CLAUDE_RUNNER_CLAUDE_BIN", "claude")
	workdir := envOrDefault("CLAUDE_RUNNER_WORKDIR", ".")
	permissionMode := envOrDefault("CLAUDE_RUNNER_PERMISSION_MODE", "default")
	outputFormat := envOrDefault("CLAUDE_RUNNER_OUTPUT_FORMAT", "text")

	args := []string{
		"--print",
		"--output-format", outputFormat,
		"--permission-mode", permissionMode,
	}
	if model := os.Getenv("CLAUDE_RUNNER_MODEL"); model != "" {
		args = append(args, "--model", model)
	}
	if tools := os.Getenv("CLAUDE_RUNNER_TOOLS"); tools != "" {
		args = append(args, "--tools", tools)
	}
	if allowedTools := os.Getenv("CLAUDE_RUNNER_ALLOWED_TOOLS"); allowedTools != "" {
		args = append(args, "--allowedTools", allowedTools)
	}
	if disallowedTools := os.Getenv("CLAUDE_RUNNER_DISALLOWED_TOOLS"); disallowedTools != "" {
		args = append(args, "--disallowedTools", disallowedTools)
	}
	if extra := os.Getenv("CLAUDE_RUNNER_EXTRA_ARGS"); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}
	args = append(args, buildPrompt(req))

	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Dir = workdir
	raw, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", time.Since(start), ctx.Err()
		}
		return "", time.Since(start), fmt.Errorf("claude failed: %w: %s", err, truncate(string(raw), 2000))
	}
	return string(raw), time.Since(start), nil
}

func buildPrompt(req request) string {
	base := strings.TrimSpace(os.Getenv("CLAUDE_RUNNER_SYSTEM_PROMPT"))
	if base == "" {
		base = "You are replying to a WhatsApp group through chat-bridge. Keep the reply concise and plain text."
	}
	if envBool("CLAUDE_RUNNER_IMPORTANT_ONLY", false) {
		base = base + "\n\nWhatsApp notification policy: send a reply only when there is an important update: a plan/checklist change, a blocker, a question requiring the user, or a final summary. Do not narrate routine tool calls, command output, or minor progress. If there is no important update for WhatsApp, reply exactly " + ignoreMarker() + "."
	}
	return fmt.Sprintf("%s\n\nSender: %s\nChat: %s\n\nMessage:\n%s\n", base, req.SenderID, req.ChatID, req.Text)
}

func writeResponse(resp response) {
	_ = json.NewEncoder(os.Stdout).Encode(resp)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch value {
	case "":
		return fallback
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func shouldIgnoreAnswer(answer string) bool {
	return strings.TrimSpace(answer) == ignoreMarker()
}

func ignoreMarker() string {
	if marker := strings.TrimSpace(os.Getenv("CLAUDE_RUNNER_IGNORE_MARKER")); marker != "" {
		return marker
	}
	return "[[chat-bridge-ignore]]"
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
