package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
)

type serviceOptions struct {
	SessionID    string
	Profile      string
	Format       string
	ConsumerID   string
	PollInterval time.Duration
	StaleAfter   time.Duration
	RestartDelay time.Duration
	Takeover     bool
	DryRun       bool
}

type resolvedServiceOptions struct {
	serviceOptions
	ConfigPath string
	Executable string
	HomeDir    string
	LogPath    string
}

type serviceTarget struct {
	Platform          string
	Label             string
	DefinitionPath    string
	Definition        string
	LogPath           string
	InstallCommands   [][]string
	UninstallCommands [][]string
	StartCommands     [][]string
	StopCommands      [][]string
	StatusCommands    [][]string
}

func (s *cliState) serviceCommand() *cobra.Command {
	opts := serviceOptions{
		Format:       "prompt",
		PollInterval: 500 * time.Millisecond,
		StaleAfter:   activeWatcherStatusStaleAfter,
		RestartDelay: 2 * time.Second,
		Takeover:     true,
	}
	service := &cobra.Command{
		Use:   "service",
		Short: "Manage active-session watcher services",
		Long: `Install and control an OS user service that keeps an active-session
inbox watcher connected for one profile/session pair.`,
	}
	service.PersistentFlags().StringVar(&opts.SessionID, "session-id", "", "active session id to watch")
	service.PersistentFlags().StringVar(&opts.Profile, "profile", "", "profile to use; defaults to config app.profile")
	service.PersistentFlags().StringVar(&opts.Format, "format", "prompt", "watch output format: prompt or jsonl")
	service.PersistentFlags().StringVar(&opts.ConsumerID, "consumer-id", "", "watcher identity; defaults to coderoam-service:<profile>:<session>")
	service.PersistentFlags().DurationVar(&opts.PollInterval, "poll-interval", 500*time.Millisecond, "how often the watcher polls for unread input")
	service.PersistentFlags().DurationVar(&opts.StaleAfter, "stale-after", activeWatcherStatusStaleAfter, "heartbeat age after which another watcher can replace this one")
	service.PersistentFlags().DurationVar(&opts.RestartDelay, "restart-delay", 2*time.Second, "initial delay before restarting a failed watcher")
	service.PersistentFlags().BoolVar(&opts.Takeover, "takeover", true, "replace stale or existing watcher locks for this session")
	service.PersistentFlags().BoolVar(&opts.DryRun, "dry-run", false, "print generated files and commands without changing OS service state")

	for _, action := range []string{"install", "uninstall", "start", "stop", "status"} {
		action := action
		service.AddCommand(&cobra.Command{
			Use:   action,
			Short: serviceActionShort(action),
			RunE: func(cmd *cobra.Command, args []string) error {
				return s.runServiceAction(cmd.Context(), action, opts)
			},
		})
	}

	run := &cobra.Command{
		Use:    "run",
		Short:  "Run the active-session watcher service loop",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.runServiceWatcher(cmd.Context(), opts)
		},
	}
	service.AddCommand(run)
	return service
}

func serviceActionShort(action string) string {
	switch action {
	case "install":
		return "Install the active-session watcher service"
	case "uninstall":
		return "Remove the active-session watcher service"
	case "start":
		return "Start the active-session watcher service"
	case "stop":
		return "Stop the active-session watcher service"
	case "status":
		return "Show active-session watcher service status"
	default:
		return "Manage the active-session watcher service"
	}
}

func (s *cliState) runServiceAction(ctx context.Context, action string, opts serviceOptions) error {
	resolved, cfg, err := s.resolveServiceOptions()
	if err != nil {
		return err
	}
	resolved.serviceOptions = normalizeServiceOptions(opts, cfg.App.Profile)
	if err := validateServiceOptions(resolved.serviceOptions); err != nil {
		return err
	}
	cfg.App.Profile = resolved.Profile
	target, err := buildServiceTarget(runtime.GOOS, resolved)
	if err != nil {
		return err
	}
	if opts.DryRun {
		printServiceDryRun(os.Stdout, action, target)
		return nil
	}
	switch action {
	case "install":
		if target.DefinitionPath != "" {
			if err := os.MkdirAll(filepath.Dir(target.DefinitionPath), 0o700); err != nil {
				return err
			}
			if target.LogPath != "" {
				if err := os.MkdirAll(filepath.Dir(target.LogPath), 0o700); err != nil {
					return err
				}
			}
			if err := os.WriteFile(target.DefinitionPath, []byte(target.Definition), 0o600); err != nil {
				return err
			}
			fmt.Printf("service_definition: %s\n", target.DefinitionPath)
		}
		if err := runServiceCommands(ctx, target.InstallCommands); err != nil {
			return err
		}
		fmt.Printf("installed service=%s platform=%s\n", target.Label, target.Platform)
	case "uninstall":
		if err := runServiceCommands(ctx, target.StopCommands); err != nil {
			fmt.Fprintf(os.Stderr, "stop before uninstall failed: %s\n", err)
		}
		if err := runServiceCommands(ctx, target.UninstallCommands); err != nil {
			return err
		}
		if target.DefinitionPath != "" {
			if err := os.Remove(target.DefinitionPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			fmt.Printf("removed service_definition: %s\n", target.DefinitionPath)
		}
		fmt.Printf("uninstalled service=%s platform=%s\n", target.Label, target.Platform)
	case "start":
		if err := runServiceCommands(ctx, target.StartCommands); err != nil {
			return err
		}
		fmt.Printf("started service=%s platform=%s\n", target.Label, target.Platform)
	case "stop":
		if err := runServiceCommands(ctx, target.StopCommands); err != nil {
			return err
		}
		fmt.Printf("stopped service=%s platform=%s\n", target.Label, target.Platform)
	case "status":
		if err := runServiceCommands(ctx, target.StatusCommands); err != nil {
			fmt.Printf("native_status: error (%s)\n", err)
		}
		if err := s.printServiceWatcherStatus(ctx, cfg, resolved.SessionID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported service action %q", action)
	}
	return nil
}

func (s *cliState) resolveServiceOptions() (resolvedServiceOptions, config.Config, error) {
	cfg, path, err := s.loadConfig()
	if err != nil {
		return resolvedServiceOptions{}, config.Config{}, err
	}
	executable, err := os.Executable()
	if err != nil || strings.TrimSpace(executable) == "" {
		executable = "coderoam"
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = "."
	}
	return resolvedServiceOptions{
		ConfigPath: path,
		Executable: executable,
		HomeDir:    home,
		LogPath:    config.DefaultLogPath(),
	}, cfg, nil
}

func normalizeServiceOptions(opts serviceOptions, defaultProfile string) serviceOptions {
	opts.SessionID = strings.TrimSpace(opts.SessionID)
	opts.Profile = strings.TrimSpace(nonEmpty(opts.Profile, defaultProfile))
	opts.Format = strings.TrimSpace(opts.Format)
	if opts.Format == "" {
		opts.Format = "prompt"
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 500 * time.Millisecond
	}
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = activeWatcherStatusStaleAfter
	}
	if opts.RestartDelay <= 0 {
		opts.RestartDelay = 2 * time.Second
	}
	return opts
}

func validateServiceOptions(opts serviceOptions) error {
	if opts.SessionID == "" {
		return fmt.Errorf("--session-id is required")
	}
	if opts.Profile == "" {
		return fmt.Errorf("--profile is required")
	}
	if opts.Format != "prompt" && opts.Format != "jsonl" {
		return fmt.Errorf("unsupported service format %q", opts.Format)
	}
	return nil
}

func (s *cliState) runServiceWatcher(ctx context.Context, opts serviceOptions) error {
	resolved, cfg, err := s.resolveServiceOptions()
	if err != nil {
		return err
	}
	resolved.serviceOptions = normalizeServiceOptions(opts, cfg.App.Profile)
	if err := validateServiceOptions(resolved.serviceOptions); err != nil {
		return err
	}
	cfg.App.Profile = resolved.Profile
	consumerID := strings.TrimSpace(resolved.ConsumerID)
	if consumerID == "" {
		consumerID = defaultServiceConsumerID(resolved.Profile, resolved.SessionID)
	}
	delay := resolved.RestartDelay
	for {
		store, err := db.Open(config.ResolveDatabasePath(cfg))
		if err == nil {
			_, _ = store.ExpireActiveWatchers(ctx, cfg.App.Profile, resolved.StaleAfter)
			if recovered, recoverErr := store.RecoverAbandonedActiveInbox(ctx, cfg.App.Profile, resolved.SessionID, activeInboxClaimStaleAfter); recoverErr == nil && recovered > 0 {
				fmt.Fprintf(os.Stderr, "[service] recovered_claims=%d session=%s\n", recovered, resolved.SessionID)
			}
			started := time.Now()
			err = watchActiveInbox(ctx, store, cfg, inboxWatchOptions{
				SessionID:         resolved.SessionID,
				Format:            resolved.Format,
				ConsumerID:        consumerID,
				PollInterval:      resolved.PollInterval,
				HeartbeatInterval: 2 * time.Second,
				StaleAfter:        resolved.StaleAfter,
				Takeover:          resolved.Takeover,
			}, os.Stdout, os.Stderr)
			_ = store.Close()
			if err == nil || ctx.Err() != nil {
				return nil
			}
			if time.Since(started) > time.Minute {
				delay = resolved.RestartDelay
			}
		}
		fmt.Fprintf(os.Stderr, "[service] watcher error: %s; restarting in %s\n", err, delay)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

func buildServiceTarget(goos string, opts resolvedServiceOptions) (serviceTarget, error) {
	if err := validateServiceOptions(opts.serviceOptions); err != nil {
		return serviceTarget{}, err
	}
	safeProfile := serviceSafeName(opts.Profile)
	safeSession := serviceSafeName(opts.SessionID)
	args := serviceRunArguments(opts)
	switch goos {
	case "darwin":
		label := "com.coderoam.watcher." + safeProfile + "." + safeSession
		path := filepath.Join(opts.HomeDir, "Library", "LaunchAgents", label+".plist")
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		logPath := serviceLogPath(opts.LogPath, safeProfile, safeSession)
		return serviceTarget{
			Platform:       goos,
			Label:          label,
			DefinitionPath: path,
			Definition:     launchAgentPlist(label, args, logPath),
			LogPath:        logPath,
			StartCommands:  [][]string{{"launchctl", "bootstrap", domain, path}},
			StopCommands:   [][]string{{"launchctl", "bootout", domain, path}},
			StatusCommands: [][]string{{"launchctl", "print", domain + "/" + label}},
		}, nil
	case "linux":
		unit := "coderoam-watcher-" + safeProfile + "-" + safeSession + ".service"
		path := filepath.Join(opts.HomeDir, ".config", "systemd", "user", unit)
		return serviceTarget{
			Platform:          goos,
			Label:             unit,
			DefinitionPath:    path,
			Definition:        systemdUserUnit(unit, args),
			InstallCommands:   [][]string{{"systemctl", "--user", "daemon-reload"}},
			UninstallCommands: [][]string{{"systemctl", "--user", "daemon-reload"}},
			StartCommands:     [][]string{{"systemctl", "--user", "enable", "--now", unit}},
			StopCommands:      [][]string{{"systemctl", "--user", "disable", "--now", unit}},
			StatusCommands:    [][]string{{"systemctl", "--user", "status", unit, "--no-pager"}},
		}, nil
	case "windows":
		taskName := `\coderoam\watcher-` + safeProfile + "-" + safeSession
		runLine := formatWindowsCommand(args)
		return serviceTarget{
			Platform:          goos,
			Label:             taskName,
			InstallCommands:   [][]string{{"schtasks", "/Create", "/TN", taskName, "/SC", "ONLOGON", "/RL", "LIMITED", "/F", "/TR", runLine}},
			UninstallCommands: [][]string{{"schtasks", "/Delete", "/TN", taskName, "/F"}},
			StartCommands:     [][]string{{"schtasks", "/Run", "/TN", taskName}},
			StopCommands:      [][]string{{"schtasks", "/End", "/TN", taskName}},
			StatusCommands:    [][]string{{"schtasks", "/Query", "/TN", taskName, "/FO", "LIST"}},
		}, nil
	default:
		return serviceTarget{}, fmt.Errorf("unsupported service platform %q", goos)
	}
}

func serviceRunArguments(opts resolvedServiceOptions) []string {
	args := []string{opts.Executable}
	if opts.ConfigPath != "" {
		args = append(args, "--config", opts.ConfigPath)
	}
	args = append(args,
		"service", "run",
		"--session-id", opts.SessionID,
		"--profile", opts.Profile,
		"--format", opts.Format,
		"--poll-interval", opts.PollInterval.String(),
		"--stale-after", opts.StaleAfter.String(),
		"--restart-delay", opts.RestartDelay.String(),
	)
	if opts.Takeover {
		args = append(args, "--takeover")
	}
	if strings.TrimSpace(opts.ConsumerID) != "" {
		args = append(args, "--consumer-id", opts.ConsumerID)
	}
	return args
}

func launchAgentPlist(label string, args []string, logPath string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", xmlEscape(label))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range args {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlEscape(arg))
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	fmt.Fprintf(&b, "\t<key>StandardOutPath</key>\n\t<string>%s</string>\n", xmlEscape(logPath))
	fmt.Fprintf(&b, "\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n", xmlEscape(logPath))
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func systemdUserUnit(name string, args []string) string {
	return fmt.Sprintf(`[Unit]
Description=coderoam active-session watcher %s
After=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
`, name, formatSystemdCommand(args))
}

func printServiceDryRun(w io.Writer, action string, target serviceTarget) {
	fmt.Fprintf(w, "service_action: %s\n", action)
	fmt.Fprintf(w, "platform: %s\n", target.Platform)
	fmt.Fprintf(w, "label: %s\n", target.Label)
	if target.DefinitionPath != "" {
		fmt.Fprintf(w, "definition_path: %s\n", target.DefinitionPath)
		fmt.Fprintln(w, "definition:")
		fmt.Fprint(w, target.Definition)
	}
	for _, command := range serviceCommandsForAction(action, target) {
		fmt.Fprintf(w, "command: %s\n", formatPOSIXCommand(command))
	}
}

func serviceCommandsForAction(action string, target serviceTarget) [][]string {
	switch action {
	case "install":
		return target.InstallCommands
	case "uninstall":
		return append(append([][]string{}, target.StopCommands...), target.UninstallCommands...)
	case "start":
		return target.StartCommands
	case "stop":
		return target.StopCommands
	case "status":
		return target.StatusCommands
	default:
		return nil
	}
}

func runServiceCommands(ctx context.Context, commands [][]string) error {
	for _, args := range commands {
		if len(args) == 0 {
			continue
		}
		fmt.Printf("running: %s\n", formatPOSIXCommand(args))
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	return nil
}

func (s *cliState) printServiceWatcherStatus(ctx context.Context, cfg config.Config, sessionID string) error {
	store, err := db.Open(config.ResolveDatabasePath(cfg))
	if err != nil {
		fmt.Printf("watcher_status: database_error (%s)\n", err)
		return nil
	}
	defer store.Close()
	if _, err := store.ExpireActiveWatchers(ctx, cfg.App.Profile, activeWatcherStatusStaleAfter); err != nil {
		return err
	}
	watcher, err := store.GetActiveWatcher(ctx, cfg.App.Profile, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		fmt.Println("watcher_status: none")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("watcher_status: %s\n", watcher.Status)
	fmt.Printf("watcher_consumer: %s\n", watcher.ConsumerID)
	fmt.Printf("watcher_heartbeat: %s\n", watcher.HeartbeatAt.Format(time.RFC3339))
	return nil
}

func defaultServiceConsumerID(profile string, sessionID string) string {
	return "coderoam-service:" + serviceSafeName(profile) + ":" + serviceSafeName(sessionID)
}

func serviceSafeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if allowed {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "default"
	}
	return out
}

func serviceLogPath(baseLogPath string, profile string, sessionID string) string {
	dir := filepath.Dir(baseLogPath)
	return filepath.Join(dir, "coderoam-watcher-"+profile+"-"+sessionID+".log")
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func formatSystemdCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, systemdQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func systemdQuote(value string) string {
	if value == "" {
		return `""`
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '-', '_', '.', '/', ':', '@', '=':
			continue
		default:
			escaped := strings.ReplaceAll(value, `\`, `\\`)
			escaped = strings.ReplaceAll(escaped, `"`, `\"`)
			return `"` + escaped + `"`
		}
	}
	return value
}

func formatWindowsCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, windowsQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func windowsQuote(value string) string {
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\"") {
		return value
	}
	escaped := strings.ReplaceAll(value, `"`, `\"`)
	return `"` + escaped + `"`
}

func formatPOSIXCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}
