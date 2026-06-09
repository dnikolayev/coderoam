package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const AppName = "coderoam"
const legacyAppName = "chat-bridge"

const (
	GroupModeRunner        = "runner"
	GroupModeActiveSession = "active-session"
)

type Config struct {
	App         AppConfig               `toml:"app"`
	Transport   TransportConfig         `toml:"transport"`
	Trigger     TriggerConfig           `toml:"trigger"`
	Active      ActiveConfig            `toml:"active"`
	Security    SecurityConfig          `toml:"security"`
	RateLimits  RateLimitConfig         `toml:"rate_limits"`
	Reply       ReplyConfig             `toml:"reply"`
	Session     SessionConfig           `toml:"session"`
	Retention   RetentionConfig         `toml:"retention"`
	Concurrency ConcurrencyConfig       `toml:"concurrency"`
	Runner      map[string]RunnerConfig `toml:"runner"`
	Groups      []GroupConfig           `toml:"groups"`
}

type AppConfig struct {
	Profile      string `toml:"profile"`
	DatabasePath string `toml:"database_path"`
	LogLevel     string `toml:"log_level"`
}

type TransportConfig struct {
	Type                          string `toml:"type"`
	LoginMethod                   string `toml:"login_method"`
	BotToken                      string `toml:"bot_token"`
	AppToken                      string `toml:"app_token"`
	DownloadMedia                 bool   `toml:"download_media"`
	TranscribeAudio               bool   `toml:"transcribe_audio"`
	AudioTranscribeCommand        string `toml:"audio_transcribe_command"`
	AudioTranscribeTimeoutSeconds int    `toml:"audio_transcribe_timeout_seconds"`
}

type TriggerConfig struct {
	Mode          string `toml:"mode"`
	Prefix        string `toml:"prefix"`
	ReplyToBridge bool   `toml:"reply_to_bridge"`
	AlwaysOn      bool   `toml:"always_on"`
	AllowOwn      bool   `toml:"allow_own_messages"`
}

type ActiveConfig struct {
	FallbackDelaySeconds int    `toml:"fallback_delay_seconds"`
	FallbackBatchLimit   int    `toml:"fallback_batch_limit"`
	AckMode              string `toml:"ack_mode"`
}

type SecurityConfig struct {
	LocalOnly                bool     `toml:"local_only"`
	RequireGroupAllowlist    bool     `toml:"require_group_allowlist"`
	RequireSenderAllowlist   bool     `toml:"require_sender_allowlist"`
	AdminSenderIDs           []string `toml:"admin_sender_ids"`
	AllowedSenderIDs         []string `toml:"allowed_sender_ids"`
	RedactPhoneNumbersInLogs bool     `toml:"redact_phone_numbers_in_logs"`
	StoreSessionsEncrypted   bool     `toml:"store_sessions_encrypted"`
}

type RateLimitConfig struct {
	MaxRepliesPerMinute int `toml:"max_replies_per_minute"`
	MaxRepliesPerHour   int `toml:"max_replies_per_hour"`
	MaxParallelGroups   int `toml:"max_parallel_groups"`
	MaxRunnerSeconds    int `toml:"max_runner_seconds"`
	MaxResponseChars    int `toml:"max_response_chars"`
}

type ReplyConfig struct {
	QuoteOriginal   bool   `toml:"quote_original"`
	TypingIndicator bool   `toml:"typing_indicator"`
	MaxChunks       int    `toml:"max_chunks"`
	ChunkSeparator  string `toml:"chunk_separator"`
}

type SessionConfig struct {
	IdleTimeoutSeconds int    `toml:"idle_timeout_seconds"`
	MaxHistoryMessages int    `toml:"max_history_messages"`
	ResetCommand       string `toml:"reset_command"`
}

type RetentionConfig struct {
	StoreMessageText              bool `toml:"store_message_text"`
	StoreMessageHash              bool `toml:"store_message_hash"`
	DeleteMessagesAfterDays       int  `toml:"delete_messages_after_days"`
	DeleteRunnerPayloadsAfterDays int  `toml:"delete_runner_payloads_after_days"`
	StoreMedia                    bool `toml:"store_media"`
}

type ConcurrencyConfig struct {
	GlobalMaxInflight     int    `toml:"global_max_inflight"`
	PerGroupMaxInflight   int    `toml:"per_group_max_inflight"`
	RunnerMaxInflight     int    `toml:"runner_max_inflight"`
	QueueMaxDepthPerGroup int    `toml:"queue_max_depth_per_group"`
	QueueOverflowPolicy   string `toml:"queue_overflow_policy"`
}

type RunnerConfig struct {
	Mode               string            `toml:"mode"`
	Command            string            `toml:"command"`
	Args               []string          `toml:"args"`
	WorkingDir         string            `toml:"working_dir"`
	TimeoutSeconds     int               `toml:"timeout_seconds"`
	Env                map[string]string `toml:"env"`
	RestartOnCrash     bool              `toml:"restart_on_crash"`
	MaxRestartsPerHour int               `toml:"max_restarts_per_hour"`
}

type GroupConfig struct {
	ID              string `toml:"id"`
	Alias           string `toml:"alias"`
	Runner          string `toml:"runner"`
	Mode            string `toml:"mode"`
	ActiveSessionID string `toml:"active_session_id"`
	Enabled         bool   `toml:"enabled"`
	RelayManaged    bool   `toml:"relay_managed"`
	Archived        bool   `toml:"archived"`
	ArchivedAt      string `toml:"archived_at"`
	ArchiveReason   string `toml:"archive_reason"`
}

func ActiveSessionID(group GroupConfig) string {
	if sessionID := strings.TrimSpace(group.ActiveSessionID); sessionID != "" {
		return sessionID
	}
	if alias := strings.TrimSpace(group.Alias); alias != "" {
		return alias
	}
	return strings.TrimSpace(group.ID)
}

func Default() Config {
	appName := RuntimeAppName()
	return Config{
		App: AppConfig{
			Profile:      "bot",
			DatabasePath: appName + ".sqlite3",
			LogLevel:     "info",
		},
		Transport: TransportConfig{
			Type:        "whatsapp-web",
			LoginMethod: "qr",
		},
		Trigger: TriggerConfig{
			Mode:          "prefix",
			Prefix:        "@bridge",
			ReplyToBridge: true,
		},
		Active: ActiveConfig{
			FallbackDelaySeconds: 2,
			FallbackBatchLimit:   8,
			AckMode:              "minimal",
		},
		Security: SecurityConfig{
			LocalOnly:                true,
			RequireGroupAllowlist:    true,
			RedactPhoneNumbersInLogs: true,
			// Encrypted-at-rest session storage is not implemented yet, so this
			// defaults to false rather than implying protection that does not exist.
			StoreSessionsEncrypted: false,
		},
		RateLimits: RateLimitConfig{
			MaxRepliesPerMinute: 6,
			MaxRepliesPerHour:   120,
			MaxParallelGroups:   5,
			MaxRunnerSeconds:    120,
			MaxResponseChars:    8000,
		},
		Reply: ReplyConfig{
			QuoteOriginal:   true,
			TypingIndicator: true,
			MaxChunks:       5,
			ChunkSeparator:  "\n\n[continued]\n\n",
		},
		Session: SessionConfig{
			IdleTimeoutSeconds: 1800,
			MaxHistoryMessages: 30,
			ResetCommand:       "@bridge reset",
		},
		Retention: RetentionConfig{
			StoreMessageText:              true,
			StoreMessageHash:              true,
			DeleteMessagesAfterDays:       30,
			DeleteRunnerPayloadsAfterDays: 30,
		},
		Concurrency: ConcurrencyConfig{
			GlobalMaxInflight:     5,
			PerGroupMaxInflight:   1,
			RunnerMaxInflight:     3,
			QueueMaxDepthPerGroup: 50,
			QueueOverflowPolicy:   "drop_oldest_with_notice",
		},
		Runner: map[string]RunnerConfig{
			"default": {
				Mode:           "process-once-json",
				Command:        "",
				Args:           []string{},
				TimeoutSeconds: 120,
				Env:            map[string]string{},
			},
		},
	}
}

func Load(path string) (Config, error) {
	if path == "" {
		path = DefaultConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	ApplyDefaults(&cfg)
	return cfg, nil
}

func LoadOrDefault(path string) (Config, string, error) {
	if path == "" {
		path = DefaultConfigPath()
	}
	cfg, err := Load(path)
	if err == nil {
		return cfg, path, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		return cfg, path, nil
	}
	return Config{}, path, err
}

func Save(path string, cfg Config) error {
	if path == "" {
		path = DefaultConfigPath()
	}
	ApplyDefaults(&cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func ApplyDefaults(cfg *Config) {
	defaults := Default()
	if cfg.App.Profile == "" {
		cfg.App.Profile = defaults.App.Profile
	}
	if cfg.App.DatabasePath == "" {
		cfg.App.DatabasePath = defaults.App.DatabasePath
	}
	if cfg.App.LogLevel == "" {
		cfg.App.LogLevel = defaults.App.LogLevel
	}
	if cfg.Transport.Type == "" {
		cfg.Transport.Type = defaults.Transport.Type
	}
	if cfg.Transport.LoginMethod == "" {
		cfg.Transport.LoginMethod = defaults.Transport.LoginMethod
	}
	if cfg.Trigger.Mode == "" {
		cfg.Trigger.Mode = defaults.Trigger.Mode
	}
	if cfg.Trigger.Prefix == "" {
		cfg.Trigger.Prefix = defaults.Trigger.Prefix
	}
	if cfg.Active.FallbackDelaySeconds <= 0 {
		cfg.Active.FallbackDelaySeconds = defaults.Active.FallbackDelaySeconds
	}
	if cfg.Active.FallbackBatchLimit <= 0 {
		cfg.Active.FallbackBatchLimit = defaults.Active.FallbackBatchLimit
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Active.AckMode)) {
	case "", "minimal":
		cfg.Active.AckMode = "minimal"
	case "verbose":
		cfg.Active.AckMode = "verbose"
	case "off":
		cfg.Active.AckMode = "off"
	default:
		cfg.Active.AckMode = defaults.Active.AckMode
	}
	if cfg.RateLimits.MaxRunnerSeconds <= 0 {
		cfg.RateLimits.MaxRunnerSeconds = defaults.RateLimits.MaxRunnerSeconds
	}
	if cfg.RateLimits.MaxResponseChars <= 0 {
		cfg.RateLimits.MaxResponseChars = defaults.RateLimits.MaxResponseChars
	}
	if cfg.RateLimits.MaxRepliesPerMinute <= 0 {
		cfg.RateLimits.MaxRepliesPerMinute = defaults.RateLimits.MaxRepliesPerMinute
	}
	if cfg.Reply.MaxChunks <= 0 {
		cfg.Reply.MaxChunks = defaults.Reply.MaxChunks
	}
	if cfg.Reply.ChunkSeparator == "" {
		cfg.Reply.ChunkSeparator = defaults.Reply.ChunkSeparator
	}
	if cfg.Runner == nil {
		cfg.Runner = map[string]RunnerConfig{}
	}
	if _, ok := cfg.Runner["default"]; !ok {
		cfg.Runner["default"] = defaults.Runner["default"]
	}
}

func RuntimeAppName() string {
	if strings.Contains(filepath.Base(os.Args[0]), legacyAppName) {
		return legacyAppName
	}
	return AppName
}

func DefaultConfigPath() string {
	appName := RuntimeAppName()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Application Support", appName, "config.toml")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, appName, "config.toml")
		}
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, appName, "config.toml")
		}
	}
	return filepath.Join(homeDir(), ".config", appName, "config.toml")
}

func DefaultDataDir() string {
	appName := RuntimeAppName()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Application Support", appName)
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, appName)
		}
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, appName)
		}
	}
	return filepath.Join(homeDir(), ".local", "share", appName)
}

func DefaultLogPath() string {
	appName := RuntimeAppName()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Logs", appName, appName+".log")
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, appName, "logs", appName+".log")
		}
	default:
		if state := os.Getenv("XDG_STATE_HOME"); state != "" {
			return filepath.Join(state, appName, appName+".log")
		}
	}
	return filepath.Join(homeDir(), ".local", "state", appName, appName+".log")
}

func ProfileDir(profile string) string {
	if profile == "" {
		profile = Default().App.Profile
	}
	return filepath.Join(DefaultDataDir(), "profiles", cleanPathPart(profile))
}

func ResolveDatabasePath(cfg Config) string {
	if filepath.IsAbs(cfg.App.DatabasePath) {
		return cfg.App.DatabasePath
	}
	return filepath.Join(ProfileDir(cfg.App.Profile), cfg.App.DatabasePath)
}

func SessionStorePath(profile string) string {
	return filepath.Join(ProfileDir(profile), "whatsapp-session.sqlite3")
}

func MediaStorePath(profile string) string {
	return filepath.Join(ProfileDir(profile), "media")
}

func KillSwitchPath() string {
	return filepath.Join(filepath.Dir(DefaultConfigPath()), "KILL")
}

func EnsureProfileDirs(profile string) error {
	if err := os.MkdirAll(ProfileDir(profile), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(MediaStorePath(profile), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(DefaultLogPath()), 0o700); err != nil {
		return err
	}
	return nil
}

func FindGroup(cfg Config, chatID string) (GroupConfig, bool) {
	for _, group := range cfg.Groups {
		if group.ID == chatID && group.Enabled {
			if group.Mode == "" {
				group.Mode = GroupModeRunner
			}
			if group.Mode == GroupModeRunner && group.Runner == "" {
				group.Runner = "default"
			}
			return group, true
		}
	}
	return GroupConfig{}, false
}

func UpsertGroup(cfg *Config, group GroupConfig) {
	if group.Mode == "" {
		group.Mode = GroupModeRunner
	}
	if group.Mode == GroupModeRunner && group.Runner == "" {
		group.Runner = "default"
	}
	for i := range cfg.Groups {
		if cfg.Groups[i].ID == group.ID {
			cfg.Groups[i] = group
			return
		}
	}
	cfg.Groups = append(cfg.Groups, group)
}

func UpsertActiveSessionGroup(cfg *Config, group GroupConfig) {
	group.Mode = GroupModeActiveSession
	group.Enabled = true
	group.Archived = false
	group.ArchivedAt = ""
	group.ArchiveReason = ""
	for i := range cfg.Groups {
		if cfg.Groups[i].ID == group.ID ||
			(group.Alias != "" && cfg.Groups[i].Alias == group.Alias) ||
			(group.ActiveSessionID != "" && cfg.Groups[i].Mode == GroupModeActiveSession && ActiveSessionID(cfg.Groups[i]) == group.ActiveSessionID) {
			cfg.Groups[i] = group
			return
		}
	}
	cfg.Groups = append(cfg.Groups, group)
}

func DenyGroup(cfg *Config, chatID string) bool {
	for i := range cfg.Groups {
		if cfg.Groups[i].ID == chatID {
			cfg.Groups[i].Enabled = false
			return true
		}
	}
	return false
}

func RunnerTimeout(cfg RunnerConfig, fallbackSeconds int) time.Duration {
	seconds := cfg.TimeoutSeconds
	if seconds <= 0 {
		seconds = fallbackSeconds
	}
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func ValidateRunner(id string, runner RunnerConfig) error {
	if id == "" {
		return fmt.Errorf("runner id is required")
	}
	if runner.Mode == "" {
		return fmt.Errorf("runner %q mode is required", id)
	}
	if runner.Command == "" {
		return fmt.Errorf("runner %q command is required", id)
	}
	switch runner.Mode {
	case "process-once-text", "process-once-json", "process-jsonl":
		return nil
	default:
		return fmt.Errorf("unsupported runner mode %q", runner.Mode)
	}
}

func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "."
}

func cleanPathPart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, string(filepath.Separator), "_")
	if value == "" {
		return "default"
	}
	return value
}
