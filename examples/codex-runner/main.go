package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	answer, duration, err := runCodex(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		writeResponse(response{
			Version:   "1.0",
			RequestID: req.RequestID,
			Actions:   []action{{Type: "error", Text: "Codex runner failed. Check local logs."}},
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

func runCodex(req request) (string, time.Duration, error) {
	start := time.Now()
	timeout := envDuration("CODEX_RUNNER_TIMEOUT_SECONDS", 600*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "chat-bridge-codex-runner-*")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(tmpDir)

	outputPath := filepath.Join(tmpDir, "last-message.txt")
	codexBin := envOrDefault("CODEX_RUNNER_CODEX_BIN", "codex")
	workdir := envOrDefault("CODEX_RUNNER_WORKDIR", ".")
	sandbox := envOrDefault("CODEX_RUNNER_SANDBOX", "read-only")
	resumeMode := strings.ToLower(strings.TrimSpace(os.Getenv("CODEX_RUNNER_RESUME")))
	sessionID := strings.TrimSpace(os.Getenv("CODEX_RUNNER_SESSION_ID"))

	args := buildCodexArgs(workdir, sandbox, outputPath, resumeMode, sessionID)
	if model := os.Getenv("CODEX_RUNNER_MODEL"); model != "" {
		args = append(args, "--model", model)
	}
	if extra := os.Getenv("CODEX_RUNNER_EXTRA_ARGS"); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}
	args = append(args, "-")

	prompt := buildPrompt(req)
	cmd := exec.CommandContext(ctx, codexBin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", time.Since(start), ctx.Err()
		}
		return "", time.Since(start), fmt.Errorf("codex exec failed: %w: %s", err, truncate(stderr.String(), 2000))
	}
	raw, err := os.ReadFile(outputPath)
	if err != nil {
		return "", time.Since(start), err
	}
	return string(raw), time.Since(start), nil
}

func buildCodexArgs(workdir, sandbox, outputPath, resumeMode, sessionID string) []string {
	if resumeMode == "" && sessionID == "" {
		return []string{
			"exec",
			"--cd", workdir,
			"--sandbox", sandbox,
			"--skip-git-repo-check",
			"--output-last-message", outputPath,
		}
	}
	args := []string{
		"exec",
		"resume",
		"--skip-git-repo-check",
		"--output-last-message", outputPath,
	}
	if envBool("CODEX_RUNNER_RESUME_ALL", false) {
		args = append(args, "--all")
	}
	if sessionID != "" {
		args = append(args, sessionID)
	} else {
		args = append(args, "--last")
	}
	return args
}

func buildPrompt(req request) string {
	base := strings.TrimSpace(os.Getenv("CODEX_RUNNER_SYSTEM_PROMPT"))
	if base == "" {
		base = "You are replying to a WhatsApp group through chat-bridge. Keep the reply concise and plain text."
	}
	if envBool("CODEX_RUNNER_IMPORTANT_ONLY", false) {
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
	if marker := strings.TrimSpace(os.Getenv("CODEX_RUNNER_IGNORE_MARKER")); marker != "" {
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
