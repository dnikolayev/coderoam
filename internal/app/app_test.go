package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/transport/fake"
	"github.com/dnikolayev/coderoam/internal/types"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	err = fn()
	if closeErr := writer.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	os.Stdout = original
	var out bytes.Buffer
	if _, copyErr := io.Copy(&out, reader); copyErr != nil && err == nil {
		err = copyErr
	}
	return out.String(), err
}

func TestBuildRunnerPresetEnablesImportantOnlyForResumableAssistants(t *testing.T) {
	tests := []struct {
		name         string
		sessionID    string
		importantKey string
		markerKey    string
	}{
		{
			name:         "codex-active",
			importantKey: "CODEX_RUNNER_IMPORTANT_ONLY",
			markerKey:    "CODEX_RUNNER_IGNORE_MARKER",
		},
		{
			name:         "codex-session",
			sessionID:    "019e-session",
			importantKey: "CODEX_RUNNER_IMPORTANT_ONLY",
			markerKey:    "CODEX_RUNNER_IGNORE_MARKER",
		},
		{
			name:         "claude",
			importantKey: "CLAUDE_RUNNER_IMPORTANT_ONLY",
			markerKey:    "CLAUDE_RUNNER_IGNORE_MARKER",
		},
		{
			name:         "claude-code",
			importantKey: "CLAUDE_RUNNER_IMPORTANT_ONLY",
			markerKey:    "CLAUDE_RUNNER_IGNORE_MARKER",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, err := buildRunnerPreset(tt.name, t.TempDir(), 120, "", "", tt.sessionID, "", nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := runner.Env[tt.importantKey]; got != "true" {
				t.Fatalf("%s = %q, want true", tt.importantKey, got)
			}
			if got := runner.Env[tt.markerKey]; got != "[[coderoam-ignore]]" {
				t.Fatalf("%s = %q, want default marker", tt.markerKey, got)
			}
		})
	}
}

func TestBuildRunnerPresetCodexCodingPresetsAreNonInteractive(t *testing.T) {
	for _, preset := range []string{"codex-code", "codex-active", "codex-session"} {
		t.Run(preset, func(t *testing.T) {
			runner, err := buildRunnerPreset(preset, t.TempDir(), 120, "", "", "session-id", "", nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := runner.Env["CODEX_RUNNER_APPROVAL_POLICY"]; got != "never" {
				t.Fatalf("CODEX_RUNNER_APPROVAL_POLICY = %q, want never", got)
			}
		})
	}
}

func TestBuildRunnerPresetAgentCliPresetsUseGenericRunner(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    string
	}{
		{name: "opencode", command: "opencode", args: `["run"]`},
		{name: "gemini", command: "gemini", args: `["-p"]`},
		{name: "agent", command: "my-agent", args: `["--once"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, err := buildRunnerPreset(tt.name, t.TempDir(), 120, "", "", "", tt.command, []string{"--once"}, "")
			if tt.name != "agent" {
				runner, err = buildRunnerPreset(tt.name, t.TempDir(), 120, "", "", "", "", nil, "")
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(runner.Command, "agent-runner") {
				t.Fatalf("command = %q, want agent-runner", runner.Command)
			}
			if got := runner.Env["AGENT_RUNNER_COMMAND"]; got != tt.command {
				t.Fatalf("AGENT_RUNNER_COMMAND = %q, want %q", got, tt.command)
			}
			if got := runner.Env["AGENT_RUNNER_ARGS_JSON"]; got != tt.args {
				t.Fatalf("AGENT_RUNNER_ARGS_JSON = %q, want %q", got, tt.args)
			}
			if got := runner.Env["AGENT_RUNNER_IMPORTANT_ONLY"]; got != "true" {
				t.Fatalf("AGENT_RUNNER_IMPORTANT_ONLY = %q, want true", got)
			}
		})
	}
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	state := &cliState{}
	cmd := state.versionCommand()
	out, err := captureStdout(t, cmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "coderoam dev") {
		t.Fatalf("version output = %q", out)
	}
}

func TestSetupCommandPrintsMessengerConnectionHowTo(t *testing.T) {
	state := &cliState{}
	cmd := state.setupCommand()
	out, err := captureStdout(t, cmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"coderoam needs a connected messenger",
		"coderoam auth login --profile bot --qr",
		"coderoam active start",
		"--accept-session-risk",
		"coderoam inbox watch --format prompt --session-id codex-session",
		"https://github.com/dnikolayev/coderoam/blob/main/docs/SETUP.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup output missing %q:\n%s", want, out)
		}
	}
}

func TestSetupCommandDetectsAgentClientsAndPrintsSelectionCommands(t *testing.T) {
	originalLookPath := commandLookPath
	commandLookPath = func(name string) (string, error) {
		switch name {
		case "codex":
			return "/usr/local/bin/codex", nil
		case "gemini":
			return "/opt/bin/gemini", nil
		default:
			return "", os.ErrNotExist
		}
	}
	defer func() { commandLookPath = originalLookPath }()

	state := &cliState{}
	cmd := state.setupCommand()
	cmd.SetArgs([]string{"--agent", "auto", "--workdir", "/workspace/project", "--session-id", "claims-qa"})
	out, err := captureStdout(t, cmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Detected agent clients:",
		"Codex: found at /usr/local/bin/codex",
		"Gemini: found at /opt/bin/gemini",
		"Claude: not found",
		"coderoam runners preset codex-active --id codex-active --workdir /workspace/project --yes",
		"coderoam runners preset gemini-code --id gemini-code --workdir /workspace/project --yes",
		"coderoam active start --name \"Codex Session\"",
		"--alias claims-qa --session-id claims-qa --runner codex-active --yes",
		"docs/agents/codex.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup output missing %q:\n%s", want, out)
		}
	}
}

func TestSetupCommandCanShowSelectedMissingAgent(t *testing.T) {
	originalLookPath := commandLookPath
	commandLookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}
	defer func() { commandLookPath = originalLookPath }()

	state := &cliState{}
	cmd := state.setupCommand()
	cmd.SetArgs([]string{"--agent", "claude", "--workdir", "/workspace/project", "--session-id", "review"})
	out, err := captureStdout(t, cmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Claude: not found",
		"coderoam runners preset claude-code --id claude-code --workdir /workspace/project --yes",
		"--alias review --session-id review --runner claude-code --yes",
		"docs/agents/claude.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Codex:") || strings.Contains(out, "Gemini:") {
		t.Fatalf("selected-agent setup should not print all candidates:\n%s", out)
	}
}

func TestAuthLoginRequiresSessionRiskAcknowledgementWithoutFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	state := &cliState{configPath: filepath.Join(t.TempDir(), "config.toml")}
	cmd := state.authCommand()
	cmd.SetArgs([]string{"login", "--profile", "test", "--open-qr=false"})
	cmd.SetIn(strings.NewReader(""))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected session-risk acknowledgement error")
	}
	if !strings.Contains(err.Error(), "--accept-session-risk") {
		t.Fatalf("auth login error = %q, want --accept-session-risk guidance", err)
	}
}

func TestSessionRiskAcknowledgementNotNeededWhenSessionExists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureProfileDirs("test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.SessionStorePath("test"), []byte("session"), 0o600); err != nil {
		t.Fatal(err)
	}
	needed, err := sessionRiskAcknowledgementNeeded("test")
	if err != nil {
		t.Fatal(err)
	}
	if needed {
		t.Fatal("acknowledgement should not be needed when session database exists")
	}
}

func TestServiceCommandDryRunPrintsInstallPlan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Default()
	cfg.App.Profile = "bot"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "coderoam.sqlite3")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	state := &cliState{configPath: path}
	cmd := state.serviceCommand()
	cmd.SetArgs([]string{"install", "--session-id", "codex-session", "--profile", "bot", "--dry-run"})
	out, err := captureStdout(t, cmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"service_action: install",
		"codex-session",
		"service",
		"run",
		"--profile",
		"bot",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("service dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestBuildServiceTargetDarwinLaunchAgent(t *testing.T) {
	opts := resolvedServiceOptions{
		serviceOptions: normalizeServiceOptions(serviceOptions{
			SessionID:    "codex-session",
			Profile:      "bot",
			Format:       "prompt",
			PollInterval: 500 * time.Millisecond,
			StaleAfter:   15 * time.Second,
			RestartDelay: 2 * time.Second,
			Takeover:     true,
		}, "bot"),
		ConfigPath: "/Users/nick/Library/Application Support/coderoam/config.toml",
		Executable: "/usr/local/bin/coderoam",
		HomeDir:    "/Users/nick",
		LogPath:    "/Users/nick/Library/Logs/coderoam/coderoam.log",
	}
	target, err := buildServiceTarget("darwin", opts)
	if err != nil {
		t.Fatal(err)
	}
	if target.Label != "com.coderoam.watcher.bot.codex-session" {
		t.Fatalf("label = %q", target.Label)
	}
	if !strings.HasSuffix(target.DefinitionPath, "Library/LaunchAgents/com.coderoam.watcher.bot.codex-session.plist") {
		t.Fatalf("definition path = %q", target.DefinitionPath)
	}
	for _, want := range []string{
		"<key>ProgramArguments</key>",
		"<string>/usr/local/bin/coderoam</string>",
		"<string>service</string>",
		"<string>run</string>",
		"<string>--takeover</string>",
		"coderoam-watcher-bot-codex-session.log",
	} {
		if !strings.Contains(target.Definition, want) {
			t.Fatalf("launch agent missing %q:\n%s", want, target.Definition)
		}
	}
	if got := strings.Join(target.StartCommands[0], " "); !strings.Contains(got, "launchctl bootstrap") {
		t.Fatalf("start command = %q", got)
	}
}

func TestBuildServiceTargetLinuxSystemdUnit(t *testing.T) {
	opts := resolvedServiceOptions{
		serviceOptions: normalizeServiceOptions(serviceOptions{
			SessionID:    "codex-session",
			Profile:      "bot",
			Format:       "prompt",
			PollInterval: time.Second,
			StaleAfter:   20 * time.Second,
			RestartDelay: 3 * time.Second,
			Takeover:     true,
		}, "bot"),
		ConfigPath: "/home/nick/.config/coderoam/config.toml",
		Executable: "/home/nick/bin/coderoam",
		HomeDir:    "/home/nick",
		LogPath:    "/home/nick/.local/state/coderoam/coderoam.log",
	}
	target, err := buildServiceTarget("linux", opts)
	if err != nil {
		t.Fatal(err)
	}
	if target.Label != "coderoam-watcher-bot-codex-session.service" {
		t.Fatalf("label = %q", target.Label)
	}
	for _, want := range []string{
		"ExecStart=/home/nick/bin/coderoam --config /home/nick/.config/coderoam/config.toml service run",
		"--session-id codex-session",
		"--profile bot",
		"Restart=always",
	} {
		if !strings.Contains(target.Definition, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, target.Definition)
		}
	}
	if got := strings.Join(target.StartCommands[0], " "); got != "systemctl --user enable --now coderoam-watcher-bot-codex-session.service" {
		t.Fatalf("start command = %q", got)
	}
}

func TestBuildServiceTargetWindowsSchtasksCommands(t *testing.T) {
	opts := resolvedServiceOptions{
		serviceOptions: normalizeServiceOptions(serviceOptions{
			SessionID:    "codex-session",
			Profile:      "bot",
			Format:       "prompt",
			PollInterval: time.Second,
			StaleAfter:   20 * time.Second,
			RestartDelay: 3 * time.Second,
			Takeover:     true,
		}, "bot"),
		ConfigPath: `C:\Users\Nick\AppData\Roaming\coderoam\config.toml`,
		Executable: `C:\Program Files\coderoam\coderoam.exe`,
		HomeDir:    `C:\Users\Nick`,
		LogPath:    `C:\Users\Nick\AppData\Local\coderoam\logs\coderoam.log`,
	}
	target, err := buildServiceTarget("windows", opts)
	if err != nil {
		t.Fatal(err)
	}
	if target.Label != `\coderoam\watcher-bot-codex-session` {
		t.Fatalf("label = %q", target.Label)
	}
	create := strings.Join(target.InstallCommands[0], " ")
	for _, want := range []string{
		"schtasks /Create",
		`/TN \coderoam\watcher-bot-codex-session`,
		`"C:\Program Files\coderoam\coderoam.exe"`,
		"service run",
		"--takeover",
	} {
		if !strings.Contains(create, want) {
			t.Fatalf("schtasks create command missing %q:\n%s", want, create)
		}
	}
}

func TestStatusShowsSetupHintWhenMessengerNotLinked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "coderoam.sqlite3")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	state := &cliState{configPath: path}
	out, err := captureStdout(t, func() error {
		return state.printStatus(t.Context())
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"transport: not_configured",
		"setup_next: run `coderoam setup`",
		"https://github.com/dnikolayev/coderoam/blob/main/docs/SETUP.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorWarnsAboutBroadProfileAndSessionPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not portable on Windows")
	}
	t.Setenv("HOME", t.TempDir())
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.Transport.Type = "fake"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "coderoam.sqlite3")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.EnsureProfileDirs(cfg.App.Profile); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(config.ProfileDir(cfg.App.Profile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.SessionStorePath(cfg.App.Profile), []byte("session"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	state := &cliState{configPath: path}
	cmd := state.doctorCommand()
	out, err := captureStdout(t, cmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"profile_dir_permissions: warn",
		"chmod 700",
		"session_file_permissions: warn",
		"chmod 600",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestPlannedTransportStatusExplainsUnavailableAdapter(t *testing.T) {
	cfg := config.Default()
	cfg.Transport.Type = "telegram"
	state := &cliState{}
	chatTransport, err := state.buildTransport(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	status, err := chatTransport.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Connected {
		t.Fatal("planned transport should not report connected")
	}
	if !strings.Contains(status.Detail, "not implemented") {
		t.Fatalf("status detail = %q, want not implemented guidance", status.Detail)
	}
	if err := chatTransport.Connect(t.Context()); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("connect error = %v, want not implemented", err)
	}
}

func TestApprovalsCommandsListShowAndApprove(t *testing.T) {
	dir := t.TempDir()
	runnerScript := filepath.Join(dir, "runner.sh")
	if err := os.WriteFile(runnerScript, []byte(`#!/bin/sh
printf '%s\n' '{"version":"1.0","actions":[{"type":"reply","text":"runner: approved"}]}'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(dir, "bridge.sqlite3")
	cfg.Transport.Type = "fake"
	cfg.Groups = []config.GroupConfig{{
		ID:      "chat@g.us",
		Alias:   "session",
		Runner:  "default",
		Mode:    config.GroupModeRunner,
		Enabled: true,
	}}
	cfg.Runner["default"] = config.RunnerConfig{
		Mode:    "process-once-json",
		Command: "/bin/sh",
		Args:    []string{runnerScript},
	}
	path := filepath.Join(dir, "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.CreatePendingInteraction(t.Context(), db.PendingInteractionRecord{
		ProfileID: cfg.App.Profile,
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		RunnerID:  "default",
		Prompt:    "Approve?",
		Options:   []string{"approved", "rejected"},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	state := &cliState{configPath: path}
	listCmd := state.approvalsCommand()
	listCmd.SetArgs([]string{"list"})
	listOut, err := captureStdout(t, listCmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOut, "Approve?") {
		t.Fatalf("approval list output = %q", listOut)
	}

	showCmd := state.approvalsCommand()
	showCmd.SetArgs([]string{"show", strconv.FormatInt(id, 10)})
	showOut, err := captureStdout(t, showCmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showOut, "Approve?") {
		t.Fatalf("approval show output = %q", showOut)
	}

	approveCmd := state.approvalsCommand()
	approveCmd.SetArgs([]string{"approve", strconv.FormatInt(id, 10)})
	approveOut, err := captureStdout(t, approveCmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(approveOut, "approval") {
		t.Fatalf("approval approve output = %q", approveOut)
	}

	store, err = db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, ok, err := store.GetPendingInteraction(t.Context(), cfg.App.Profile, id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("approval record missing")
	}
	if record.Status != "answered" || record.SelectedText != "approved" {
		t.Fatalf("approval record = %+v, want answered/approved", record)
	}
}

func TestSendPendingActiveOutboxUsesBridgeTransport(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id, err := store.QueueActiveOutbox(t.Context(), cfg.App.Profile, "chat@g.us", "important update", true)
	if err != nil {
		t.Fatal(err)
	}
	ft := fake.New(nil)
	sent, err := sendPendingActiveOutbox(t.Context(), store, ft, cfg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if len(ft.Sent) != 1 || ft.Sent[0].Text != "important update" {
		t.Fatalf("fake sent = %+v", ft.Sent)
	}
	pending, err := store.PendingActiveOutbox(t.Context(), cfg.App.Profile, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending count = %d, want 0", len(pending))
	}
	if id == 0 {
		t.Fatal("queued outbox id was zero")
	}
}

func TestSendPendingActiveReadReceiptsUsesBridgeTransport(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:       "wa-read",
		ChatID:   "chat@g.us",
		SenderID: "sender@s.whatsapp.net",
		Text:     "hello",
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), cfg.App.Profile, "codex-session", "codex-session", msg); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), cfg.App.Profile, "codex-session"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("expected claimed row")
	}
	ft := fake.New(nil)
	sent, err := sendPendingActiveReadReceipts(t.Context(), store, ft, cfg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if len(ft.Read) != 1 || ft.Read[0].MessageID != "wa-read" {
		t.Fatalf("fake read receipts = %+v", ft.Read)
	}
	pending, err := store.PendingActiveReadReceipts(t.Context(), cfg.App.Profile, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending read receipts = %d, want 0", len(pending))
	}
}

func TestWatchActiveInboxClaimsMatchingSessionJSONL(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msgA := types.IncomingMessage{
		ID:       "wa-session-a",
		ChatID:   "chat-a@g.us",
		SenderID: "sender@s.whatsapp.net",
		Text:     "hello session a",
		RawText:  "hello session a",
		Media: []types.MediaAttachment{{
			Type:            "voice",
			MIMEType:        "audio/ogg; codecs=opus",
			Size:            1234,
			DurationSeconds: 5,
			LocalPath:       filepath.Join(t.TempDir(), "voice.ogg"),
		}},
	}
	msgB := types.IncomingMessage{
		ID:       "wa-session-b",
		ChatID:   "chat-b@g.us",
		SenderID: "sender@s.whatsapp.net",
		Text:     "hello session b",
		RawText:  "hello session b",
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), cfg.App.Profile, "alias-a", "session-a", msgA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), cfg.App.Profile, "alias-b", "session-b", msgB); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = watchActiveInbox(t.Context(), store, cfg, inboxWatchOptions{
		SessionID:         "session-a",
		Format:            "jsonl",
		ConsumerID:        "test-consumer",
		PollInterval:      time.Millisecond,
		HeartbeatInterval: time.Millisecond,
		StaleAfter:        time.Second,
		MaxMessages:       1,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type               string                  `json:"type"`
		Text               string                  `json:"text"`
		SessionID          string                  `json:"session_id"`
		ClaimedBySessionID string                  `json:"claimed_by_session_id"`
		Media              []types.MediaAttachment `json:"media"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &event); err != nil {
		t.Fatalf("jsonl output %q: %v", stdout.String(), err)
	}
	if event.Type != "message" || event.Text != msgA.Text || event.SessionID != "session-a" || event.ClaimedBySessionID != "session-a" {
		t.Fatalf("event = %+v", event)
	}
	if len(event.Media) != 1 || event.Media[0].Type != "voice" || event.Media[0].LocalPath == "" {
		t.Fatalf("event media = %+v", event.Media)
	}
	claimed, err := store.ListActiveInbox(t.Context(), cfg.App.Profile, "claimed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ExternalMessageID != msgA.ID {
		t.Fatalf("claimed = %+v", claimed)
	}
	unread, err := store.ListActiveInbox(t.Context(), cfg.App.Profile, "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ExternalMessageID != msgB.ID {
		t.Fatalf("unread = %+v", unread)
	}
	receipts, err := store.PendingActiveReadReceipts(t.Context(), cfg.App.Profile, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].ExternalMessageID != msgA.ID {
		t.Fatalf("read receipts = %+v", receipts)
	}
	watcher, err := store.GetActiveWatcher(t.Context(), cfg.App.Profile, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if watcher.Status != "stopped" {
		t.Fatalf("watcher status = %q, want stopped", watcher.Status)
	}
}

func TestWatchActiveInboxSkipsStaleClaimedRow(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	dbPath := filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.App.DatabasePath = dbPath
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	previous := types.IncomingMessage{
		ID:        "wa-previous",
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "previous",
		RawText:   "previous",
		Timestamp: time.Now().Add(-time.Minute),
	}
	current := types.IncomingMessage{
		ID:        "wa-current",
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "current",
		RawText:   "current",
		Timestamp: time.Now(),
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), cfg.App.Profile, "codex-session", "codex-session", previous); err != nil {
		t.Fatal(err)
	}
	claimedPrevious, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), cfg.App.Profile, "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected previous row to be claimed")
	}
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	if _, err := rawDB.ExecContext(t.Context(), `UPDATE active_inbox SET claimed_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).Format(time.RFC3339Nano), claimedPrevious.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), cfg.App.Profile, "codex-session", "codex-session", current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = watchActiveInbox(t.Context(), store, cfg, inboxWatchOptions{
		SessionID:         "codex-session",
		Format:            "jsonl",
		ConsumerID:        "test-consumer",
		PollInterval:      time.Millisecond,
		HeartbeatInterval: time.Millisecond,
		StaleAfter:        time.Second,
		MaxMessages:       1,
	}, &stdout, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Text              string `json:"text"`
		ExternalMessageID string `json:"external_message_id"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &event); err != nil {
		t.Fatalf("jsonl output %q: %v", stdout.String(), err)
	}
	if event.Text != "current" || event.ExternalMessageID != "wa-current" {
		t.Fatalf("event = %+v", event)
	}
	claimed, err := store.ListActiveInbox(t.Context(), cfg.App.Profile, "claimed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 || claimed[0].ExternalMessageID != "wa-previous" || claimed[1].ExternalMessageID != "wa-current" {
		t.Fatalf("claimed rows = %+v", claimed)
	}
}

func TestWriteInboxRecordIncludesLocalAudioAttachment(t *testing.T) {
	cfg := config.Default()
	record := db.ActiveInboxRecord{
		ID:                7,
		ChatID:            "chat@g.us",
		ChatAlias:         "codex-session",
		SessionID:         "codex-session",
		SenderID:          "sender@s.whatsapp.net",
		ExternalMessageID: "wa-voice",
		Text:              "[voice] mime=audio/ogg; codecs=opus seconds=5",
		RawText:           "[voice] mime=audio/ogg; codecs=opus seconds=5",
		Media: []types.MediaAttachment{{
			Type:            "voice",
			MIMEType:        "audio/ogg; codecs=opus",
			DurationSeconds: 5,
			LocalPath:       "/tmp/voice.ogg",
		}},
		ReceivedAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
	}
	var out bytes.Buffer
	if err := writeInboxRecord(&out, record, "prompt", cfg); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Attachments:", "local_path: /tmp/voice.ogg", "transcribe it before applying"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
}

func TestWriteInboxRecordIncludesAudioTranscript(t *testing.T) {
	cfg := config.Default()
	record := db.ActiveInboxRecord{
		ID:                9,
		ChatID:            "chat@g.us",
		ChatAlias:         "codex-session",
		SessionID:         "codex-session",
		SenderID:          "sender@s.whatsapp.net",
		ExternalMessageID: "wa-voice-transcript",
		Text:              "[voice] mime=audio/ogg; codecs=opus seconds=5 transcript=ship it",
		RawText:           "[voice] mime=audio/ogg; codecs=opus seconds=5 transcript=ship it",
		Media: []types.MediaAttachment{{
			Type:            "voice",
			MIMEType:        "audio/ogg; codecs=opus",
			DurationSeconds: 5,
			LocalPath:       "/tmp/voice.ogg",
			Transcript:      "ship it",
		}},
		ReceivedAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
	}
	var out bytes.Buffer
	if err := writeInboxRecord(&out, record, "prompt", cfg); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Attachments:", "local_path: /tmp/voice.ogg", "transcript: ship it"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "transcribe it before applying") {
		t.Fatalf("prompt should not request transcription when transcript is present: %q", got)
	}
}

func TestWriteInboxRecordDefersSlashCommandWhenAudioAttached(t *testing.T) {
	cfg := config.Default()
	cfg.Security.AdminSenderIDs = []string{"sender@s.whatsapp.net"}
	record := db.ActiveInboxRecord{
		ID:                8,
		ChatID:            "chat@g.us",
		ChatAlias:         "codex-session",
		SessionID:         "codex-session",
		SenderID:          "sender@s.whatsapp.net",
		ExternalMessageID: "wa-voice-goal",
		Text:              "/goal ship it\n\n[voice] mime=audio/ogg; codecs=opus seconds=5",
		RawText:           "/goal ship it",
		Media: []types.MediaAttachment{{
			Type:            "voice",
			MIMEType:        "audio/ogg; codecs=opus",
			DurationSeconds: 5,
			LocalPath:       "/tmp/voice.ogg",
		}},
		ReceivedAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
	}
	var out bytes.Buffer
	if err := writeInboxRecord(&out, record, "prompt", cfg); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Detected Codex command: /goal", "sender is authorized", "do not execute this slash command until the voice/audio transcript confirms it", "Goal objective candidate: ship it"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "Treat this as an explicit user goal request from WhatsApp.") {
		t.Fatalf("audio slash command should not be immediately executable: %q", got)
	}
}

func TestGroupsSetRunnerPreservesActiveSessionMode(t *testing.T) {
	cfg := config.Default()
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Runner["codex-session"] = config.RunnerConfig{
		Mode:    "process-once-json",
		Command: os.Args[0],
	}
	cfg.Groups = []config.GroupConfig{{
		ID:              "chat@g.us",
		Alias:           "codex-session",
		Runner:          "codex-active",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "codex-session",
		Enabled:         true,
	}}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	state := &cliState{configPath: path}
	cmd := state.groupsCommand()
	cmd.SetArgs([]string{"set-runner", "chat@g.us", "codex-session"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	updated, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	group, ok := config.FindGroup(updated, "chat@g.us")
	if !ok {
		t.Fatal("group not found")
	}
	if group.Runner != "codex-session" {
		t.Fatalf("runner = %q, want codex-session", group.Runner)
	}
	if group.Mode != config.GroupModeActiveSession {
		t.Fatalf("mode = %q, want active-session", group.Mode)
	}
	if group.ActiveSessionID != "codex-session" {
		t.Fatalf("active session id = %q, want codex-session", group.ActiveSessionID)
	}
}

func TestActiveStartCreatesParallelSessionGroup(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Transport.Type = "fake"
	cfg.Runner["codex-active"] = config.RunnerConfig{
		Mode:    "process-once-json",
		Command: os.Args[0],
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	ft := fake.New(nil)
	state := &cliState{
		configPath: path,
		transportFactory: func(context.Context, config.Config) (transport.ChatTransport, error) {
			return ft, nil
		},
	}
	cmd := state.activeCommand()
	cmd.SetArgs([]string{
		"start",
		"--name", "Parallel Work",
		"--participants", "+15550001111",
		"--alias", "parallel-work",
		"--session-id", "parallel-work",
		"--runner", "codex-active",
		"--yes",
	})
	out, err := captureStdout(t, cmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	group, ok := resolveGroup(updated, "parallel-work")
	if !ok {
		t.Fatal("active group not configured")
	}
	if group.ID != "fake-1@g.us" {
		t.Fatalf("group id = %q, want fake-1@g.us", group.ID)
	}
	if group.Mode != config.GroupModeActiveSession {
		t.Fatalf("mode = %q, want active-session", group.Mode)
	}
	if group.Runner != "codex-active" {
		t.Fatalf("runner = %q, want codex-active", group.Runner)
	}
	if group.ActiveSessionID != "parallel-work" {
		t.Fatalf("active session id = %q, want parallel-work", group.ActiveSessionID)
	}
	if !group.Enabled {
		t.Fatal("group should be enabled")
	}
	inviteLinks := ft.InviteLinksSnapshot()
	if len(inviteLinks) != 1 || inviteLinks[0].ChatID != "fake-1@g.us" || inviteLinks[0].Reset {
		t.Fatalf("invite links = %+v", inviteLinks)
	}
	sent := ft.SentSnapshot()
	if len(sent) != 1 {
		t.Fatalf("sent DMs = %+v, want one invite DM", sent)
	}
	if sent[0].ChatID != "+15550001111" {
		t.Fatalf("invite DM recipient = %q", sent[0].ChatID)
	}
	for _, want := range []string{"Join the coderoam active session group", "https://chat.whatsapp.com/fake-invite", "Open this WhatsApp link"} {
		if !strings.Contains(sent[0].Text, want) {
			t.Fatalf("invite DM missing %q:\n%s", want, sent[0].Text)
		}
	}
	if !strings.Contains(out, "sent invite") || !strings.Contains(out, "watch: coderoam inbox watch --format prompt --session-id parallel-work") {
		t.Fatalf("active start output = %q", out)
	}

	secondCmd := state.activeCommand()
	secondCmd.SetArgs([]string{
		"start",
		"--name", "Review Lane",
		"--participants", "+15550002222",
		"--alias", "review-lane",
		"--session-id", "review-lane",
		"--yes",
	})
	if _, err := captureStdout(t, secondCmd.Execute); err != nil {
		t.Fatal(err)
	}
	updated, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Groups) != 2 {
		t.Fatalf("groups = %+v, want two active session groups", updated.Groups)
	}
	secondGroup, ok := resolveGroup(updated, "review-lane")
	if !ok {
		t.Fatal("second active group not configured")
	}
	if secondGroup.ID != "fake-2@g.us" || config.ActiveSessionID(secondGroup) != "review-lane" || !secondGroup.RelayManaged || !secondGroup.Enabled {
		t.Fatalf("second active group = %+v", secondGroup)
	}
	inviteLinks = ft.InviteLinksSnapshot()
	if len(inviteLinks) != 2 || inviteLinks[1].ChatID != "fake-2@g.us" {
		t.Fatalf("invite links after second start = %+v", inviteLinks)
	}
	sent = ft.SentSnapshot()
	if len(sent) != 2 || sent[1].ChatID != "+15550002222" || !strings.Contains(sent[1].Text, "Review Lane") {
		t.Fatalf("sent DMs after second start = %+v", sent)
	}
}

func TestActiveStartDefaultsAliasAndSessionFromName(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Transport.Type = "fake"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	state := &cliState{configPath: path}
	cmd := state.activeCommand()
	cmd.SetArgs([]string{
		"start",
		"--name", "Claims QA #2",
		"--participants", "+15550001111",
		"--yes",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	updated, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	group, ok := resolveGroup(updated, "claims-qa-2")
	if !ok {
		t.Fatal("active group not configured under default alias")
	}
	if group.ActiveSessionID != "claims-qa-2" {
		t.Fatalf("active session id = %q, want claims-qa-2", group.ActiveSessionID)
	}
	if group.Runner != "" {
		t.Fatalf("runner = %q, want empty fallback runner", group.Runner)
	}
}

func TestActiveStartReactivatesArchivedManagedSessionGroup(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Transport.Type = "fake"
	cfg.Groups = []config.GroupConfig{{
		ID:              "old@g.us",
		Alias:           "claims-qa",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "claims-qa",
		RelayManaged:    true,
		Enabled:         false,
		Archived:        true,
		ArchivedAt:      time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		ArchiveReason:   "participant left",
	}}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	state := &cliState{configPath: path}
	cmd := state.activeCommand()
	cmd.SetArgs([]string{
		"start",
		"--name", "Claims QA",
		"--participants", "+15550001111",
		"--alias", "claims-qa",
		"--session-id", "claims-qa",
		"--yes",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	updated, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Groups) != 1 {
		t.Fatalf("groups = %+v, want one reactivated group", updated.Groups)
	}
	group := updated.Groups[0]
	if group.ID == "old@g.us" || !strings.HasPrefix(group.ID, "fake-") || group.Alias != "claims-qa" || config.ActiveSessionID(group) != "claims-qa" {
		t.Fatalf("reactivated group = %+v", group)
	}
	if !group.Enabled || !group.RelayManaged || group.Archived || group.ArchivedAt != "" || group.ArchiveReason != "" {
		t.Fatalf("reactivated group flags = %+v", group)
	}
}

func TestActiveEnableManagedPreservesRunner(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Transport.Type = "fake"
	cfg.Groups = []config.GroupConfig{{
		ID:              "existing@g.us",
		Alias:           "codex-session",
		Runner:          "codex-active",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "codex-session",
		Enabled:         true,
	}}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	state := &cliState{configPath: path}
	cmd := state.activeCommand()
	cmd.SetArgs([]string{
		"enable",
		"existing@g.us",
		"--alias", "codex-session",
		"--session-id", "codex-session",
		"--managed",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	updated, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	group := updated.Groups[0]
	if !group.Enabled || !group.RelayManaged || group.Runner != "codex-active" {
		t.Fatalf("managed active group = %+v", group)
	}
}

func TestActiveEnableRejectsArchivedManagedGroup(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Transport.Type = "fake"
	cfg.Groups = []config.GroupConfig{{
		ID:              "archived@g.us",
		Alias:           "archived-session",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "archived-session",
		RelayManaged:    true,
		Enabled:         false,
		Archived:        true,
		ArchiveReason:   "participant left",
	}}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	state := &cliState{configPath: path}
	cmd := state.activeCommand()
	cmd.SetArgs([]string{"enable", "archived@g.us", "--alias", "archived-session", "--session-id", "archived-session"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "use active start") {
		t.Fatalf("error = %v, want active start guidance", err)
	}
}

func TestRelayGroupLeaveArchivesManagedSessionGroup(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Groups = []config.GroupConfig{{
		ID:              "managed@g.us",
		Alias:           "managed-session",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "managed-session",
		RelayManaged:    true,
		Enabled:         true,
	}}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), cfg.App.Profile, "managed-session", "managed-session", types.IncomingMessage{
		ID:       "wa-managed",
		ChatID:   "managed@g.us",
		SenderID: "owner@s.whatsapp.net",
		Text:     "please handle this",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.QueueActiveOutbox(t.Context(), cfg.App.Profile, "managed@g.us", "pending update", true); err != nil {
		t.Fatal(err)
	}
	ft := fake.New(nil)
	updated, archived, err := handleRelayGroupLifecycleEvent(t.Context(), cfg, path, store, ft, types.GroupEvent{
		ChatID:             "managed@g.us",
		SenderID:           "owner@s.whatsapp.net",
		LeftParticipantIDs: []string{"owner@s.whatsapp.net"},
		ParticipantCount:   1,
		Timestamp:          time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !archived {
		t.Fatal("expected managed group to be archived")
	}
	if len(ft.Archived) != 1 || ft.Archived[0] != "managed@g.us" {
		t.Fatalf("archived chats = %+v", ft.Archived)
	}
	group := updated.Groups[0]
	if group.Enabled || !group.Archived || group.ArchiveReason != "participant left" || group.ArchivedAt == "" {
		t.Fatalf("archived group = %+v", group)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Groups[0].Enabled || !reloaded.Groups[0].Archived {
		t.Fatalf("persisted group = %+v", reloaded.Groups[0])
	}
	rows, err := store.ListActiveInbox(t.Context(), cfg.App.Profile, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("active inbox rows after archive = %+v", rows)
	}
	pendingOutbox, err := store.PendingActiveOutbox(t.Context(), cfg.App.Profile, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingOutbox) != 0 {
		t.Fatalf("active outbox rows after archive = %+v", pendingOutbox)
	}
}

func TestShouldArchiveRelayGroupReasons(t *testing.T) {
	cases := []struct {
		name    string
		event   types.GroupEvent
		archive bool
		reason  string
	}{
		{
			name:    "group deleted",
			event:   types.GroupEvent{Deleted: true},
			archive: true,
			reason:  "group deleted",
		},
		{
			name:    "participant left",
			event:   types.GroupEvent{LeftParticipantIDs: []string{"owner@s.whatsapp.net"}, ParticipantCount: 2},
			archive: true,
			reason:  "participant left",
		},
		{
			name:    "only bridge remains",
			event:   types.GroupEvent{ParticipantCount: 1},
			archive: true,
			reason:  "no human participants remain",
		},
		{
			name:    "ordinary group update",
			event:   types.GroupEvent{JoinedParticipantIDs: []string{"owner@s.whatsapp.net"}, ParticipantCount: 2},
			archive: false,
			reason:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archive, reason := shouldArchiveRelayGroup(tc.event)
			if archive != tc.archive || reason != tc.reason {
				t.Fatalf("shouldArchiveRelayGroup() = (%t, %q), want (%t, %q)", archive, reason, tc.archive, tc.reason)
			}
		})
	}
}

func TestExplainLastShowsLatestRouteDecision(t *testing.T) {
	cfg := config.Default()
	cfg.App.Profile = "test"
	cfg.App.DatabasePath = filepath.Join(t.TempDir(), "bridge.sqlite3")
	cfg.Transport.Type = "fake"
	cfg.Groups = []config.GroupConfig{{
		ID:              "1203630active@g.us",
		Alias:           "codex-session",
		Runner:          "codex-active",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "codex-session",
		Enabled:         true,
	}}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(cfg.App.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Audit(t.Context(), cfg.App.Profile, "route_decision", "sender@s.whatsapp.net", "1203630active@g.us", map[string]any{
		"message_id":        "wa-1",
		"sender_id":         "sender@s.whatsapp.net",
		"reason":            "active inbox fallback scheduled",
		"ignored":           false,
		"runner":            "codex-active",
		"active_session_id": "codex-session",
		"text_preview":      "continue please",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	state := &cliState{configPath: path}
	cmd := state.explainLastCommand()
	cmd.SetArgs([]string{"--chat", "codex-session"})
	out, err := captureStdout(t, cmd.Execute)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"chat: codex-session",
		"reason: active inbox fallback scheduled",
		"ignored: false",
		"runner: codex-active",
		"session: codex-session",
		"text_preview: continue please",
		"message_id: wa-1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("explain-last output missing %q:\n%s", want, out)
		}
	}
}

func TestParseInboxSlashCommandDetectsGoal(t *testing.T) {
	command, value, ok := parseInboxSlashCommand("/goal prepare project for publishing")
	if !ok {
		t.Fatal("expected slash command")
	}
	if command != "/goal" {
		t.Fatalf("command = %q, want /goal", command)
	}
	if value != "prepare project for publishing" {
		t.Fatalf("value = %q", value)
	}
}

func TestParseInboxSlashCommandIgnoresPlainText(t *testing.T) {
	_, _, ok := parseInboxSlashCommand("prepare project")
	if ok {
		t.Fatal("plain text should not be treated as slash command")
	}
}

func TestSlashCommandSenderAuthorization(t *testing.T) {
	cfg := config.Default()
	cfg.Security.AdminSenderIDs = []string{"admin@lid"}
	cfg.Security.AllowedSenderIDs = []string{"allowed@s.whatsapp.net"}
	if !isSlashCommandSenderAuthorized(cfg, "admin@lid") {
		t.Fatal("admin sender should be authorized")
	}
	if !isSlashCommandSenderAuthorized(cfg, "allowed@s.whatsapp.net") {
		t.Fatal("allowed sender should be authorized")
	}
	if isSlashCommandSenderAuthorized(cfg, "other@lid") {
		t.Fatal("unknown sender should not be authorized")
	}
}
