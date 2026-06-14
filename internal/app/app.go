package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/transport/fake"
	"github.com/dnikolayev/coderoam/internal/transport/planned"
	"github.com/dnikolayev/coderoam/internal/transport/whatsappweb"
	"github.com/dnikolayev/coderoam/internal/types"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"

	commandLookPath = exec.LookPath
)

type cliState struct {
	configPath       string
	transportFactory func(context.Context, config.Config) (transport.ChatTransport, error)
}

const activeInboxClaimStaleAfter = 15 * time.Second

const activeWatcherStatusStaleAfter = 15 * time.Second

func Execute() error {
	state := &cliState{}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := &cobra.Command{
		Use:   "coderoam",
		Short: "Local WhatsApp group bridge for CLI applications",
		Long: `coderoam connects selected WhatsApp group chats to a local CLI runner.

It is intended for local personal automation with a dedicated WhatsApp account.
The WhatsApp Web transport is unofficial and may break or put the linked account at risk.`,
	}
	root.SetContext(ctx)
	root.PersistentFlags().StringVar(&state.configPath, "config", "", "config file path")

	root.AddCommand(
		state.initCommand(),
		state.profilesCommand(),
		state.runCommand(),
		state.statusCommand(),
		state.healthCommand(),
		state.doctorCommand(),
		state.setupCommand(),
		state.runbookCommand(),
		state.versionCommand(),
		state.serviceCommand(),
		state.explainLastCommand(),
		state.pauseCommand(),
		state.resumeCommand(),
		state.killCommand(),
		state.sendCommand(),
		state.authCommand(),
		state.sendersCommand(),
		state.chatsCommand(),
		state.groupsCommand(),
		state.runnersCommand(),
		state.activeCommand(),
		state.inboxCommand(),
		state.approvalsCommand(),
		state.notifyCommand(),
		state.logsCommand(),
		state.testRouteCommand(),
	)
	return root.Execute()
}

func (s *cliState) profilesCommand() *cobra.Command {
	profiles := &cobra.Command{Use: "profiles", Short: "Manage local bridge profiles"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List local profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			profilesDir := filepath.Join(config.DefaultDataDir(), "profiles")
			entries, err := os.ReadDir(profilesDir)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.IsDir() {
					fmt.Println(entry.Name())
				}
			}
			return nil
		},
	}
	create := &cobra.Command{
		Use:   "create <profile>",
		Short: "Create a local profile directory and database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := args[0]
			if err := config.EnsureProfileDirs(profile); err != nil {
				return err
			}
			cfg := config.Default()
			cfg.App.Profile = profile
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.EnsureProfile(cmd.Context(), profile); err != nil {
				return err
			}
			fmt.Printf("created profile=%s dir=%s\n", profile, config.ProfileDir(profile))
			return nil
		},
	}
	use := &cobra.Command{
		Use:   "use <profile>",
		Short: "Set the default profile in config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := config.LoadOrDefault(s.configPath)
			if err != nil {
				return err
			}
			cfg.App.Profile = args[0]
			if err := config.EnsureProfileDirs(cfg.App.Profile); err != nil {
				return err
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			fmt.Printf("using profile=%s\n", cfg.App.Profile)
			return nil
		},
	}
	var yes bool
	deleteCmd := &cobra.Command{
		Use:   "delete <profile>",
		Short: "Delete a local profile directory after confirmation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to delete profile without --yes")
			}
			if err := os.RemoveAll(config.ProfileDir(args[0])); err != nil {
				return err
			}
			fmt.Printf("deleted profile=%s\n", args[0])
			return nil
		},
	}
	deleteCmd.Flags().BoolVar(&yes, "yes", false, "confirm profile deletion")
	profiles.AddCommand(list, create, use, deleteCmd)
	return profiles
}

func (s *cliState) initCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create config and local database",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := s.configPath
			if path == "" {
				path = config.DefaultConfigPath()
			}
			exists := false
			if _, statErr := os.Stat(path); statErr == nil {
				exists = true
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}

			var cfg config.Config
			normalizeExistingConfig := false
			if force || !exists {
				cfg = config.Default()
			} else {
				if data, readErr := os.ReadFile(path); readErr == nil && strings.Contains(string(data), "store_sessions_encrypted = true") {
					normalizeExistingConfig = true
				}
				var err error
				cfg, err = config.Load(path)
				if err != nil {
					return err
				}
			}
			if err := config.EnsureProfileDirs(cfg.App.Profile); err != nil {
				return err
			}
			if force || !exists || normalizeExistingConfig {
				if err := config.Save(path, cfg); err != nil {
					return err
				}
			}
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			err = store.EnsureProfile(cmd.Context(), cfg.App.Profile)
			if err != nil {
				return err
			}
			if exists && !force {
				fmt.Printf("config already exists: %s\n", path)
				if normalizeExistingConfig {
					fmt.Println("init: already complete; normalized unsupported store_sessions_encrypted=false")
				} else {
					fmt.Println("init: already complete; no changes made")
				}
				fmt.Println("next: run `coderoam setup` or configure a runner with `coderoam runners preset ...`")
			} else {
				fmt.Printf("config: %s\n", path)
			}
			fmt.Printf("database: %s\n", config.ResolveDatabasePath(cfg))
			fmt.Printf("whatsapp_session: %s\n", config.SessionStorePath(cfg.App.Profile))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing config")
	return cmd
}

func (s *cliState) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print coderoam version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(versionText())
			return nil
		},
	}
}

func (s *cliState) loadConfig() (config.Config, string, error) {
	cfg, path, err := config.LoadOrDefault(s.configPath)
	if err != nil {
		return config.Config{}, path, err
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		return config.Config{}, path, fmt.Errorf("config not found at %s; run coderoam init first", path)
	}
	return cfg, path, nil
}

func (s *cliState) buildTransport(ctx context.Context, cfg config.Config) (transport.ChatTransport, error) {
	if s.transportFactory != nil {
		return s.transportFactory(ctx, cfg)
	}
	switch cfg.Transport.Type {
	case "fake":
		chats := make([]types.Chat, 0, len(cfg.Groups))
		for _, group := range cfg.Groups {
			chats = append(chats, types.Chat{ID: group.ID, Type: types.ChatTypeGroup, DisplayName: group.Alias, Alias: group.Alias, Allowed: group.Enabled})
		}
		return fake.New(chats), nil
	case "telegram", "telegram-bot":
		return planned.New("telegram"), nil
	case "slack", "slack-socket-mode":
		return planned.New("slack"), nil
	case "google-chat":
		return planned.New("google-chat"), nil
	case "whatsapp-web", "":
		return whatsappweb.NewWithOptions(ctx, config.SessionStorePath(cfg.App.Profile), cfg.App.LogLevel, whatsappweb.Options{
			DownloadMedia:                 cfg.Transport.DownloadMedia,
			MediaDir:                      config.MediaStorePath(cfg.App.Profile),
			TranscribeAudio:               cfg.Transport.TranscribeAudio,
			AudioTranscribeCommand:        cfg.Transport.AudioTranscribeCommand,
			AudioTranscribeTimeoutSeconds: cfg.Transport.AudioTranscribeTimeoutSeconds,
		})
	default:
		return nil, fmt.Errorf("unsupported transport type %q", cfg.Transport.Type)
	}
}

func enabledGroups(groups []config.GroupConfig) []config.GroupConfig {
	out := []config.GroupConfig{}
	for _, group := range groups {
		if group.Enabled {
			if group.Mode == "" {
				group.Mode = config.GroupModeRunner
			}
			out = append(out, group)
		}
	}
	return out
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func isNoRunnerArg(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "-", "none", "off":
		return true
	default:
		return false
	}
}

func versionText() string {
	lines := []string{"coderoam " + nonEmpty(version, "dev")}
	if strings.TrimSpace(commit) != "" && commit != "none" {
		lines = append(lines, "commit: "+commit)
	}
	if strings.TrimSpace(date) != "" && date != "unknown" {
		lines = append(lines, "built: "+date)
	}
	return strings.Join(lines, "\n")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '-', '_', '.', '/', ':', '@':
			continue
		default:
			return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
		}
	}
	return value
}

func resolveGroup(cfg config.Config, value string) (config.GroupConfig, bool) {
	var fallback *config.GroupConfig
	for _, group := range cfg.Groups {
		if group.ID == value || group.Alias == value {
			if group.Mode == "" {
				group.Mode = config.GroupModeRunner
			}
			if group.Mode == config.GroupModeRunner && group.Runner == "" {
				group.Runner = "default"
			}
			if group.Enabled && !group.Archived {
				return group, true
			}
			copy := group
			fallback = &copy
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return config.GroupConfig{}, false
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || containsString(values, value) {
		return values
	}
	return append(values, value)
}

func oneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func interactiveReader(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
