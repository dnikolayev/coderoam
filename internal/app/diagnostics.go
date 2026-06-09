package app

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/logging"
)

func (s *cliState) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show bridge status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.printStatus(cmd.Context())
		},
	}
}

func (s *cliState) healthCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Run a local health check",
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.printStatus(cmd.Context())
		},
	}
}

func (s *cliState) explainLastCommand() *cobra.Command {
	var chatValue string
	cmd := &cobra.Command{
		Use:   "explain-last",
		Short: "Explain the latest routing decision for one chat",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			chatValue = strings.TrimSpace(chatValue)
			if chatValue == "" {
				return fmt.Errorf("--chat is required")
			}
			chatID := chatValue
			chatAlias := ""
			if group, ok := resolveGroup(cfg, chatValue); ok {
				chatID = group.ID
				chatAlias = group.Alias
			}
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			event, ok, err := store.LatestAuditEvent(cmd.Context(), cfg.App.Profile, "route_decision", chatID)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Printf("chat: %s\n", nonEmpty(chatAlias, chatValue))
				fmt.Printf("chat_id: %s\n", logging.Redact(chatID))
				fmt.Println("latest_route: none")
				return nil
			}
			details := map[string]any{}
			_ = json.Unmarshal([]byte(event.DetailsJSON), &details)
			fmt.Printf("chat: %s\n", nonEmpty(chatAlias, chatValue))
			fmt.Printf("chat_id: %s\n", logging.Redact(chatID))
			fmt.Printf("at: %s\n", event.CreatedAt.Format(time.RFC3339))
			fmt.Printf("reason: %s\n", stringDetail(details, "reason"))
			fmt.Printf("ignored: %t\n", boolDetail(details, "ignored"))
			if runner := stringDetail(details, "runner"); runner != "" {
				fmt.Printf("runner: %s\n", runner)
			}
			if sessionID := stringDetail(details, "active_session_id"); sessionID != "" {
				fmt.Printf("session: %s\n", sessionID)
			}
			if preview := stringDetail(details, "text_preview"); preview != "" {
				fmt.Printf("text_preview: %s\n", preview)
			}
			if messageID := stringDetail(details, "message_id"); messageID != "" {
				fmt.Printf("message_id: %s\n", messageID)
			}
			if senderID := stringDetail(details, "sender_id"); senderID != "" {
				fmt.Printf("sender: %s\n", logging.Redact(senderID))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&chatValue, "chat", "", "chat id or local alias")
	return cmd
}

func (s *cliState) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local bridge configuration and dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			fmt.Printf("config: ok (%s)\n", path)
			fmt.Printf("profile: %s\n", cfg.App.Profile)
			if info, err := os.Stat(config.ProfileDir(cfg.App.Profile)); err != nil {
				fmt.Printf("profile_dir: missing (%s)\n", err)
			} else {
				fmt.Printf("profile_dir: ok (%s)\n", config.ProfileDir(cfg.App.Profile))
				fmt.Println(privatePathPermissionStatus("profile_dir_permissions", config.ProfileDir(cfg.App.Profile), info, 0o700))
			}
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				fmt.Printf("database: error (%s)\n", err)
			} else {
				_ = store.Close()
				fmt.Printf("database: ok (%s)\n", config.ResolveDatabasePath(cfg))
			}
			printSessionPermissionChecks(cfg.App.Profile)
			if _, err := os.Stat(config.SessionStorePath(cfg.App.Profile)); err != nil {
				fmt.Printf("whatsapp_session_db: missing (%s)\n", config.SessionStorePath(cfg.App.Profile))
				fmt.Println("whatsapp_auth: not linked")
				fmt.Println(setupNextLine())
				return nil
			} else {
				fmt.Printf("whatsapp_session_db: present (%s)\n", config.SessionStorePath(cfg.App.Profile))
			}
			if chatTransport, err := s.buildTransport(cmd.Context(), cfg); err != nil {
				fmt.Printf("whatsapp_auth: error (%s)\n", err)
			} else {
				status, statusErr := chatTransport.Status(cmd.Context())
				_ = chatTransport.Close(context.Background())
				if statusErr != nil {
					fmt.Printf("whatsapp_auth: error (%s)\n", statusErr)
				} else if status.Account == "" {
					fmt.Println("whatsapp_auth: not linked")
					fmt.Println(setupNextLine())
				} else {
					fmt.Printf("whatsapp_auth: linked account=%s\n", logging.Redact(status.Account))
				}
			}
			for id, runnerCfg := range cfg.Runner {
				if runnerCfg.Command == "" {
					fmt.Printf("runner.%s: missing command\n", id)
					continue
				}
				if filepath.IsAbs(runnerCfg.Command) {
					if _, err := os.Stat(runnerCfg.Command); err != nil {
						fmt.Printf("runner.%s: command missing (%s)\n", id, runnerCfg.Command)
					} else {
						fmt.Printf("runner.%s: ok (%s)\n", id, runnerCfg.Command)
					}
				} else if resolved, err := exec.LookPath(runnerCfg.Command); err != nil {
					fmt.Printf("runner.%s: command not found (%s)\n", id, runnerCfg.Command)
				} else {
					fmt.Printf("runner.%s: ok (%s)\n", id, resolved)
				}
			}
			if codexPath, err := exec.LookPath("codex"); err == nil {
				fmt.Printf("codex: ok (%s)\n", codexPath)
			} else {
				fmt.Println("codex: not found")
			}
			return nil
		},
	}
}

func (s *cliState) pauseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause outbound sending through the local kill switch",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(filepath.Dir(config.KillSwitchPath()), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(config.KillSwitchPath(), []byte(time.Now().Format(time.RFC3339)+"\n"), 0o600); err != nil {
				return err
			}
			fmt.Printf("paused: %s\n", config.KillSwitchPath())
			return nil
		},
	}
}

func (s *cliState) resumeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume outbound sending by removing the local kill switch",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.Remove(config.KillSwitchPath()); err != nil && !os.IsNotExist(err) {
				return err
			}
			fmt.Println("resumed")
			return nil
		},
	}
}

func (s *cliState) killCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "kill",
		Short: "Immediately stop bridge sends by creating the kill switch",
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.pauseCommand().RunE(cmd, args)
		},
	}
}

func (s *cliState) logsCommand() *cobra.Command {
	logs := &cobra.Command{Use: "logs", Short: "Inspect local logs"}
	var lines int
	tail := &cobra.Command{
		Use:   "tail",
		Short: "Print the end of the local log file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tailFile(config.DefaultLogPath(), lines)
		},
	}
	tail.Flags().IntVar(&lines, "lines", 80, "number of lines")
	logs.AddCommand(tail)
	return logs
}

func (s *cliState) printStatus(ctx context.Context) error {
	cfg, _, err := s.loadConfig()
	if err != nil {
		return err
	}
	fmt.Printf("profile: %s\n", cfg.App.Profile)
	fmt.Printf("config: %s\n", nonEmpty(s.configPath, config.DefaultConfigPath()))
	fmt.Printf("database: %s\n", config.ResolveDatabasePath(cfg))
	fmt.Printf("allowed_groups: %d\n", len(enabledGroups(cfg.Groups)))
	if _, err := os.Stat(config.KillSwitchPath()); err == nil {
		fmt.Printf("kill_switch: active (%s)\n", config.KillSwitchPath())
	} else {
		fmt.Println("kill_switch: inactive")
	}
	store, err := db.Open(config.ResolveDatabasePath(cfg))
	if err == nil {
		defer store.Close()
		count, countErr := store.PendingOutboxCount(ctx)
		if countErr == nil {
			fmt.Printf("outbox_pending: %d\n", count)
		}
		activeCounts, activeErr := store.ActiveInboxCounts(ctx, cfg.App.Profile)
		if activeErr == nil {
			fmt.Printf("active_inbox_unread: %d\n", activeCounts["unread"])
			fmt.Printf("active_inbox_claimed: %d\n", activeCounts["claimed"])
		}
		activeOutboxPending, activeOutboxErr := store.ActiveOutboxPendingCount(ctx, cfg.App.Profile)
		if activeOutboxErr == nil {
			fmt.Printf("active_outbox_pending: %d\n", activeOutboxPending)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		fmt.Printf("database_status: %s\n", err)
	}
	if _, err := os.Stat(config.SessionStorePath(cfg.App.Profile)); errors.Is(err, os.ErrNotExist) {
		fmt.Println("transport: not_configured")
		fmt.Println(setupNextLine())
		return nil
	}
	chatTransport, err := s.buildTransport(ctx, cfg)
	if err != nil {
		fmt.Printf("transport: error (%s)\n", err)
		return nil
	}
	defer chatTransport.Close(context.Background())
	status, err := chatTransport.Status(ctx)
	if err != nil {
		fmt.Printf("transport: error (%s)\n", err)
		return nil
	}
	if strings.TrimSpace(status.Account) == "" {
		fmt.Println("transport: not_configured")
		fmt.Println(setupNextLine())
		return nil
	}
	fmt.Printf("transport: account=%s detail=%s\n", logging.Redact(status.Account), status.Detail)
	fmt.Println("transport_next: run coderoam run to open the WhatsApp connection")
	return nil
}

func tailFile(path string, lines int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if lines <= 0 {
		lines = 80
	}
	ring := make([]string, lines)
	index := 0
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		ring[index%lines] = scanner.Text()
		index++
		if count < lines {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	start := index - count
	for i := 0; i < count; i++ {
		fmt.Println(ring[(start+i)%lines])
	}
	return nil
}

func stringDetail(details map[string]any, key string) string {
	value, ok := details[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func boolDetail(details map[string]any, key string) bool {
	value, ok := details[key]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func printSessionPermissionChecks(profile string) {
	checked := 0
	warnings := 0
	for _, path := range sessionFilePaths(profile) {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Printf("session_file_permissions: error (%s: %s)\n", path, err)
			warnings++
			continue
		}
		checked++
		line := privatePathPermissionStatus("session_file_permissions", path, info, 0o600)
		if strings.Contains(line, ": warn ") || strings.Contains(line, ": error ") {
			fmt.Println(line)
			warnings++
		}
	}
	if checked == 0 {
		fmt.Println("session_file_permissions: missing")
		return
	}
	if warnings == 0 {
		fmt.Printf("session_file_permissions: ok (%d files)\n", checked)
	}
}

func privatePathPermissionStatus(label, path string, info os.FileInfo, fixMode os.FileMode) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("%s: skipped (Windows ACLs)", label)
	}
	mode := info.Mode().Perm()
	if mode&0o077 == 0 {
		return fmt.Sprintf("%s: ok (%04o)", label, mode)
	}
	return fmt.Sprintf("%s: warn (%s mode %04o; run `chmod %03o %s`)", label, path, mode, fixMode, shellQuote(path))
}
