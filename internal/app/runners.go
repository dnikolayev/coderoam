package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/runner"
)

func (s *cliState) runnersCommand() *cobra.Command {
	runners := &cobra.Command{Use: "runners", Short: "Manage local CLI runners"}
	var mode string
	var commandPath string
	var args []string
	var workingDir string
	var timeout int
	var envValues []string

	add := &cobra.Command{
		Use:   "add <id>",
		Short: "Add or update a runner",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			if cfg.Runner == nil {
				cfg.Runner = map[string]config.RunnerConfig{}
			}
			if timeout <= 0 {
				timeout = cfg.RateLimits.MaxRunnerSeconds
			}
			runnerCfg := config.RunnerConfig{
				Mode:           mode,
				Command:        commandPath,
				Args:           args,
				WorkingDir:     workingDir,
				TimeoutSeconds: timeout,
				Env:            parseEnvValues(envValues),
			}
			if err := config.ValidateRunner(cmdArgs[0], runnerCfg); err != nil {
				return err
			}
			cfg.Runner[cmdArgs[0]] = runnerCfg
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			fmt.Printf("runner %s configured\n", cmdArgs[0])
			return nil
		},
	}
	add.Flags().StringVar(&mode, "mode", "process-once-json", "runner mode")
	add.Flags().StringVar(&commandPath, "command", "", "runner executable path")
	add.Flags().StringArrayVar(&args, "arg", nil, "runner argument; repeat for multiple args")
	add.Flags().StringVar(&workingDir, "working-dir", "", "runner working directory")
	add.Flags().IntVar(&timeout, "timeout-seconds", 0, "runner timeout")
	add.Flags().StringArrayVar(&envValues, "env", nil, "runner env KEY=VALUE; repeat for multiple values")

	var presetID string
	var presetWorkdir string
	var presetTimeout int
	var presetModel string
	var presetSystemPrompt string
	var presetSessionID string
	var presetAgentCommand string
	var presetAgentArgs []string
	var presetAgentPromptMode string
	var presetYes bool
	preset := &cobra.Command{
		Use:   "preset <codex|codex-code|codex-active|codex-session|claude|claude-code|claude-session|opencode|opencode-code|gemini|gemini-code|agent|agent-code>",
		Short: "Configure a built-in CLI agent runner preset",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			presetName := args[0]
			if presetID == "" {
				presetID = "default"
			}
			if presetWorkdir == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				presetWorkdir = cwd
			}
			if presetTimeout <= 0 {
				presetTimeout = 600
			}
			if (strings.HasSuffix(presetName, "-code") || presetName == "codex-active" || presetName == "codex-session" || presetName == "claude-session") && !presetYes {
				return fmt.Errorf("coding presets can edit files; rerun with --yes to confirm")
			}
			runnerCfg, err := buildRunnerPreset(presetName, presetWorkdir, presetTimeout, presetModel, presetSystemPrompt, presetSessionID, presetAgentCommand, presetAgentArgs, presetAgentPromptMode)
			if err != nil {
				return err
			}
			if err := config.ValidateRunner(presetID, runnerCfg); err != nil {
				return err
			}
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			if cfg.Runner == nil {
				cfg.Runner = map[string]config.RunnerConfig{}
			}
			cfg.Runner[presetID] = runnerCfg
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			fmt.Printf("runner %s configured with preset=%s workdir=%s\n", presetID, presetName, presetWorkdir)
			return nil
		},
	}
	preset.Flags().StringVar(&presetID, "id", "default", "runner id to configure")
	preset.Flags().StringVar(&presetWorkdir, "workdir", "", "workspace directory for Codex/Claude")
	preset.Flags().IntVar(&presetTimeout, "timeout-seconds", 600, "runner timeout")
	preset.Flags().StringVar(&presetModel, "model", "", "model name passed to supported agent CLIs")
	preset.Flags().StringVar(&presetSystemPrompt, "system-prompt", "", "system prompt passed to the runner wrapper")
	preset.Flags().StringVar(&presetSessionID, "session-id", "", "Codex/Claude session id for session presets")
	preset.Flags().StringVar(&presetAgentCommand, "agent-command", "", "agent executable for agent/agent-code presets")
	preset.Flags().StringArrayVar(&presetAgentArgs, "agent-arg", nil, "argument passed to agent-runner before the prompt; repeat for multiple args")
	preset.Flags().StringVar(&presetAgentPromptMode, "agent-prompt-mode", "", "agent prompt delivery mode: arg or stdin")
	preset.Flags().BoolVar(&presetYes, "yes", false, "confirm a coding preset that can edit files")

	list := &cobra.Command{
		Use:   "list",
		Short: "List runners",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tMODE\tCOMMAND\tARGS\tTIMEOUT\tENV")
			for id, item := range cfg.Runner {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", id, item.Mode, item.Command, strings.Join(item.Args, " "), item.TimeoutSeconds, formatEnvKeys(item.Env))
			}
			return w.Flush()
		},
	}

	test := &cobra.Command{
		Use:   "test <id>",
		Short: "Invoke one runner locally",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			runnerCfg, ok := cfg.Runner[args[0]]
			if !ok {
				return fmt.Errorf("runner not found: %s", args[0])
			}
			text, _ := cmd.Flags().GetString("text")
			req := runner.Request{
				Version:   runner.ProtocolVersion,
				RequestID: "req_test",
				ProfileID: cfg.App.Profile,
				ChatID:    "local-test",
				SenderID:  "local-user",
				Text:      text,
				RawText:   text,
				Message: runner.MessageInfo{
					ID:        "local-test-message",
					Text:      text,
					RawText:   text,
					Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				},
			}
			processRunner := runner.NewProcessRunner(runnerCfg, cfg.RateLimits.MaxRunnerSeconds)
			result, err := processRunner.Invoke(cmd.Context(), req)
			raw, _ := json.MarshalIndent(result.Response, "", "  ")
			fmt.Println(string(raw))
			if err != nil && result.StdErr != "" {
				fmt.Fprintln(os.Stderr, result.StdErr)
			}
			return err
		},
	}
	test.Flags().String("text", "hello", "test input text")

	remove := &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a runner",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			delete(cfg.Runner, args[0])
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			fmt.Printf("runner %s removed\n", args[0])
			return nil
		},
	}
	runners.AddCommand(add, preset, list, test, remove)
	return runners
}

func parseEnvValues(values []string) map[string]string {
	env := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		env[key] = val
	}
	return env
}

func formatEnvKeys(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	return strings.Join(keys, ",")
}

func buildRunnerPreset(name, workdir string, timeoutSeconds int, model, systemPrompt, sessionID, agentCommand string, agentArgs []string, agentPromptMode string) (config.RunnerConfig, error) {
	switch name {
	case "codex":
		env := map[string]string{
			"CODEX_RUNNER_WORKDIR":         workdir,
			"CODEX_RUNNER_SANDBOX":         "read-only",
			"CODEX_RUNNER_TIMEOUT_SECONDS": strconv.Itoa(timeoutSeconds),
			"CODEX_RUNNER_SYSTEM_PROMPT":   defaultCodexPrompt(false),
		}
		if model != "" {
			env["CODEX_RUNNER_MODEL"] = model
		}
		if systemPrompt != "" {
			env["CODEX_RUNNER_SYSTEM_PROMPT"] = systemPrompt
		}
		return presetRunnerConfig("codex-runner", timeoutSeconds, env), nil
	case "codex-code":
		env := map[string]string{
			"CODEX_RUNNER_WORKDIR":         workdir,
			"CODEX_RUNNER_SANDBOX":         "workspace-write",
			"CODEX_RUNNER_APPROVAL_POLICY": "never",
			"CODEX_RUNNER_IMPORTANT_ONLY":  "true",
			"CODEX_RUNNER_IGNORE_MARKER":   "[[coderoam-ignore]]",
			"CODEX_RUNNER_TIMEOUT_SECONDS": strconv.Itoa(timeoutSeconds),
			"CODEX_RUNNER_SYSTEM_PROMPT":   defaultCodexPrompt(true),
		}
		if model != "" {
			env["CODEX_RUNNER_MODEL"] = model
		}
		if systemPrompt != "" {
			env["CODEX_RUNNER_SYSTEM_PROMPT"] = systemPrompt
		}
		return presetRunnerConfig("codex-runner", timeoutSeconds, env), nil
	case "codex-active":
		env := map[string]string{
			"CODEX_RUNNER_APPROVAL_POLICY": "never",
			"CODEX_RUNNER_WORKDIR":         workdir,
			"CODEX_RUNNER_RESUME":          "last",
			"CODEX_RUNNER_RESUME_ALL":      "true",
			"CODEX_RUNNER_IMPORTANT_ONLY":  "true",
			"CODEX_RUNNER_IGNORE_MARKER":   "[[coderoam-ignore]]",
			"CODEX_RUNNER_TIMEOUT_SECONDS": strconv.Itoa(timeoutSeconds),
			"CODEX_RUNNER_SYSTEM_PROMPT":   defaultCodexActivePrompt(),
		}
		if model != "" {
			env["CODEX_RUNNER_MODEL"] = model
		}
		if systemPrompt != "" {
			env["CODEX_RUNNER_SYSTEM_PROMPT"] = systemPrompt
		}
		return presetRunnerConfig("codex-runner", timeoutSeconds, env), nil
	case "codex-session":
		if strings.TrimSpace(sessionID) == "" {
			return config.RunnerConfig{}, fmt.Errorf("--session-id is required for codex-session")
		}
		env := map[string]string{
			"CODEX_RUNNER_APPROVAL_POLICY": "never",
			"CODEX_RUNNER_WORKDIR":         workdir,
			"CODEX_RUNNER_SESSION_ID":      strings.TrimSpace(sessionID),
			"CODEX_RUNNER_IMPORTANT_ONLY":  "true",
			"CODEX_RUNNER_IGNORE_MARKER":   "[[coderoam-ignore]]",
			"CODEX_RUNNER_TIMEOUT_SECONDS": strconv.Itoa(timeoutSeconds),
			"CODEX_RUNNER_SYSTEM_PROMPT":   defaultCodexActivePrompt(),
		}
		if model != "" {
			env["CODEX_RUNNER_MODEL"] = model
		}
		if systemPrompt != "" {
			env["CODEX_RUNNER_SYSTEM_PROMPT"] = systemPrompt
		}
		return presetRunnerConfig("codex-runner", timeoutSeconds, env), nil
	case "claude":
		env := map[string]string{
			"CLAUDE_RUNNER_WORKDIR":         workdir,
			"CLAUDE_RUNNER_PERMISSION_MODE": "default",
			"CLAUDE_RUNNER_IMPORTANT_ONLY":  "true",
			"CLAUDE_RUNNER_IGNORE_MARKER":   "[[coderoam-ignore]]",
			"CLAUDE_RUNNER_TIMEOUT_SECONDS": strconv.Itoa(timeoutSeconds),
			"CLAUDE_RUNNER_SYSTEM_PROMPT":   defaultClaudePrompt(false),
			"CLAUDE_RUNNER_OUTPUT_FORMAT":   "text",
		}
		if model != "" {
			env["CLAUDE_RUNNER_MODEL"] = model
		}
		if systemPrompt != "" {
			env["CLAUDE_RUNNER_SYSTEM_PROMPT"] = systemPrompt
		}
		return presetRunnerConfig("claude-runner", timeoutSeconds, env), nil
	case "claude-code":
		env := map[string]string{
			"CLAUDE_RUNNER_WORKDIR":         workdir,
			"CLAUDE_RUNNER_PERMISSION_MODE": "acceptEdits",
			"CLAUDE_RUNNER_IMPORTANT_ONLY":  "true",
			"CLAUDE_RUNNER_IGNORE_MARKER":   "[[coderoam-ignore]]",
			"CLAUDE_RUNNER_TIMEOUT_SECONDS": strconv.Itoa(timeoutSeconds),
			"CLAUDE_RUNNER_SYSTEM_PROMPT":   defaultClaudePrompt(true),
			"CLAUDE_RUNNER_OUTPUT_FORMAT":   "text",
		}
		if model != "" {
			env["CLAUDE_RUNNER_MODEL"] = model
		}
		if systemPrompt != "" {
			env["CLAUDE_RUNNER_SYSTEM_PROMPT"] = systemPrompt
		}
		return presetRunnerConfig("claude-runner", timeoutSeconds, env), nil
	case "claude-session":
		if strings.TrimSpace(sessionID) == "" {
			return config.RunnerConfig{}, fmt.Errorf("--session-id is required for claude-session")
		}
		env := map[string]string{
			"CLAUDE_RUNNER_WORKDIR":         workdir,
			"CLAUDE_RUNNER_SESSION_ID":      strings.TrimSpace(sessionID),
			"CLAUDE_RUNNER_PERMISSION_MODE": "acceptEdits",
			"CLAUDE_RUNNER_IMPORTANT_ONLY":  "true",
			"CLAUDE_RUNNER_IGNORE_MARKER":   "[[coderoam-ignore]]",
			"CLAUDE_RUNNER_TIMEOUT_SECONDS": strconv.Itoa(timeoutSeconds),
			"CLAUDE_RUNNER_SYSTEM_PROMPT":   defaultClaudePrompt(true),
			"CLAUDE_RUNNER_OUTPUT_FORMAT":   "text",
		}
		if model != "" {
			env["CLAUDE_RUNNER_MODEL"] = model
		}
		if systemPrompt != "" {
			env["CLAUDE_RUNNER_SYSTEM_PROMPT"] = systemPrompt
		}
		return presetRunnerConfig("claude-runner", timeoutSeconds, env), nil
	case "opencode":
		args := append([]string{"run"}, agentArgs...)
		if model != "" {
			args = append([]string{"run", "--model", model}, agentArgs...)
		}
		return agentRunnerPreset(agentPresetOptions{
			Name:           "OpenCode",
			Command:        nonEmpty(agentCommand, "opencode"),
			Args:           args,
			PromptMode:     nonEmpty(agentPromptMode, "arg"),
			Workdir:        workdir,
			TimeoutSeconds: timeoutSeconds,
			Model:          model,
			SystemPrompt:   nonEmpty(systemPrompt, defaultAgentPrompt("OpenCode", false)),
			CanEdit:        false,
		}), nil
	case "opencode-code":
		args := append([]string{"run"}, agentArgs...)
		if model != "" {
			args = append([]string{"run", "--model", model}, agentArgs...)
		}
		return agentRunnerPreset(agentPresetOptions{
			Name:           "OpenCode",
			Command:        nonEmpty(agentCommand, "opencode"),
			Args:           args,
			PromptMode:     nonEmpty(agentPromptMode, "arg"),
			Workdir:        workdir,
			TimeoutSeconds: timeoutSeconds,
			SystemPrompt:   nonEmpty(systemPrompt, defaultAgentPrompt("OpenCode", true)),
			CanEdit:        true,
		}), nil
	case "gemini":
		args := []string{"-p"}
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, agentArgs...)
		return agentRunnerPreset(agentPresetOptions{
			Name:           "Gemini",
			Command:        nonEmpty(agentCommand, "gemini"),
			Args:           args,
			PromptMode:     nonEmpty(agentPromptMode, "arg"),
			Workdir:        workdir,
			TimeoutSeconds: timeoutSeconds,
			SystemPrompt:   nonEmpty(systemPrompt, defaultAgentPrompt("Gemini", false)),
			CanEdit:        false,
		}), nil
	case "gemini-code":
		args := []string{"-p", "--approval-mode", "auto_edit"}
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, agentArgs...)
		return agentRunnerPreset(agentPresetOptions{
			Name:           "Gemini",
			Command:        nonEmpty(agentCommand, "gemini"),
			Args:           args,
			PromptMode:     nonEmpty(agentPromptMode, "arg"),
			Workdir:        workdir,
			TimeoutSeconds: timeoutSeconds,
			SystemPrompt:   nonEmpty(systemPrompt, defaultAgentPrompt("Gemini", true)),
			CanEdit:        true,
		}), nil
	case "agent", "agent-code":
		if strings.TrimSpace(agentCommand) == "" {
			return config.RunnerConfig{}, fmt.Errorf("--agent-command is required for %s", name)
		}
		canEdit := name == "agent-code"
		return agentRunnerPreset(agentPresetOptions{
			Name:           "Agent",
			Command:        agentCommand,
			Args:           agentArgs,
			PromptMode:     nonEmpty(agentPromptMode, "arg"),
			Workdir:        workdir,
			TimeoutSeconds: timeoutSeconds,
			SystemPrompt:   nonEmpty(systemPrompt, defaultAgentPrompt("Agent", canEdit)),
			CanEdit:        canEdit,
		}), nil
	default:
		return config.RunnerConfig{}, fmt.Errorf("unknown runner preset %q", name)
	}
}

type agentPresetOptions struct {
	Name           string
	Command        string
	Args           []string
	PromptMode     string
	Workdir        string
	TimeoutSeconds int
	Model          string
	SystemPrompt   string
	CanEdit        bool
}

func agentRunnerPreset(opts agentPresetOptions) config.RunnerConfig {
	env := map[string]string{
		"AGENT_RUNNER_COMMAND":         opts.Command,
		"AGENT_RUNNER_ARGS_JSON":       mustJSONStrings(opts.Args),
		"AGENT_RUNNER_PROMPT_MODE":     opts.PromptMode,
		"AGENT_RUNNER_WORKDIR":         opts.Workdir,
		"AGENT_RUNNER_TIMEOUT_SECONDS": strconv.Itoa(opts.TimeoutSeconds),
		"AGENT_RUNNER_SYSTEM_PROMPT":   opts.SystemPrompt,
		"AGENT_RUNNER_IMPORTANT_ONLY":  "true",
		"AGENT_RUNNER_IGNORE_MARKER":   "[[coderoam-ignore]]",
	}
	if opts.Model != "" {
		env["AGENT_RUNNER_MODEL"] = opts.Model
	}
	if opts.CanEdit {
		env["AGENT_RUNNER_CAN_EDIT"] = "true"
	}
	return presetRunnerConfig("agent-runner", opts.TimeoutSeconds, env)
}

func mustJSONStrings(values []string) string {
	raw, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func presetRunnerConfig(wrapperName string, timeoutSeconds int, env map[string]string) config.RunnerConfig {
	return config.RunnerConfig{
		Mode:           "process-once-json",
		Command:        siblingExecutable(wrapperName),
		Args:           []string{},
		TimeoutSeconds: timeoutSeconds,
		Env:            env,
	}
}

func siblingExecutable(name string) string {
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), name)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "bin", name)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
	}
	if resolved, err := exec.LookPath(name); err == nil {
		return resolved
	}
	return name
}

func defaultCodexPrompt(canEdit bool) string {
	if canEdit {
		return "You are Codex replying through WhatsApp. Keep replies concise enough for WhatsApp. For short chat/status/health-check messages, answer immediately without inspecting files or running commands. Only inspect files, run commands, or edit files when explicitly asked to fix, implement, inspect, test, or change something. For voice memos or audio attachments, transcribe the audio first; only apply instructions or slash commands from the audio after the transcript is available and any slash-command authorization shown in the prompt allows it. You may edit files in the configured workspace when explicitly asked. Summarize files changed, commands run, and any remaining risk. Do not run destructive commands."
	}
	return "You are Codex replying through WhatsApp. Answer concisely in plain text. For voice memos or audio attachments, transcribe the audio first; only apply instructions or slash commands from the audio after the transcript is available and any slash-command authorization shown in the prompt allows it. Do not edit files in this runner mode."
}

func defaultCodexActivePrompt() string {
	return "Continue the existing Codex session from WhatsApp. Treat the WhatsApp message as the next user turn. Keep replies concise enough for WhatsApp: summarize what changed, commands run, and any follow-up needed. For voice memos or audio attachments, transcribe the audio first; only apply instructions or slash commands from the audio after the transcript is available and any slash-command authorization shown in the prompt allows it. Do not run destructive commands."
}

func defaultClaudePrompt(canEdit bool) string {
	if canEdit {
		return "You are Claude Code replying through WhatsApp. You may edit files in the configured workspace when explicitly asked. For voice memos or audio attachments, transcribe the audio first; only apply instructions or slash commands from the audio after the transcript is available and any slash-command authorization shown in the prompt allows it. Keep replies concise: summarize files changed, commands run, and any remaining risk. Do not run destructive commands."
	}
	return "You are Claude replying through WhatsApp. Answer concisely in plain text. For voice memos or audio attachments, transcribe the audio first; only apply instructions or slash commands from the audio after the transcript is available and any slash-command authorization shown in the prompt allows it. Do not edit files in this runner mode."
}

func defaultAgentPrompt(name string, canEdit bool) string {
	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName = "the configured CLI agent"
	}
	if canEdit {
		return displayName + " is replying through WhatsApp via coderoam. Keep replies concise enough for WhatsApp. You may inspect, run commands, or edit files in the configured workspace only when explicitly asked to fix, implement, inspect, test, or change something. For voice memos or audio attachments, use available transcripts first; only apply instructions or slash commands from audio after the transcript is available and any slash-command authorization shown in the prompt allows it. Summarize files changed, commands run, and any remaining risk. Do not run destructive commands."
	}
	return displayName + " is replying through WhatsApp via coderoam. Answer concisely in plain text. For short chat/status messages, answer without inspecting files or running commands. For voice memos or audio attachments, use available transcripts first; only apply instructions or slash commands from audio after the transcript is available and any slash-command authorization shown in the prompt allows it. Do not edit files in this runner mode."
}
