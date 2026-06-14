package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/logging"
	"github.com/dnikolayev/coderoam/internal/transport/whatsappweb"
	"github.com/dnikolayev/coderoam/internal/types"
)

func (s *cliState) setupCommand() *cobra.Command {
	var messenger string
	var agent string
	var workdir string
	var sessionID string
	var profile string
	var groupName string
	var authorized string
	var printOnly bool
	var yes bool
	var openQR bool
	var qrImagePath string
	var acceptSessionRisk bool
	var noRunbook bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up a mobile coding session chat",
		Long: `Set up a messenger account, local agent runner, authorized senders, and a
dedicated session group for continuing coding sessions from mobile.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			messenger = strings.ToLower(strings.TrimSpace(messenger))
			if messenger == "" {
				messenger = "whatsapp"
			}
			if printOnly {
				if messenger != "whatsapp" {
					fmt.Printf("coderoam reserves the %s transport name, but that adapter is not implemented in this release.\n", messenger)
					fmt.Println("Connect WhatsApp now, or configure that transport after its adapter is added.")
					fmt.Println()
				}
				fmt.Print(setupHowTo())
				fmt.Print(setupAgentGuide(agent, workdir, sessionID))
				return nil
			}
			opts := setupWizardOptions{
				Messenger:         messenger,
				Agent:             agent,
				Workdir:           workdir,
				SessionID:         sessionID,
				Profile:           profile,
				GroupName:         groupName,
				Authorized:        authorized,
				Yes:               yes,
				OpenQR:            openQR,
				QRImagePath:       qrImagePath,
				AcceptSessionRisk: acceptSessionRisk,
				NoRunbook:         noRunbook,
			}
			return s.runSetupWizard(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&messenger, "messenger", "whatsapp", "messenger transport to configure")
	cmd.Flags().StringVar(&agent, "agent", "auto", "agent client to configure: auto, codex, claude, gemini, opencode, or none")
	cmd.Flags().StringVar(&workdir, "workdir", "", "workspace directory used by the selected agent")
	cmd.Flags().BoolVar(&noRunbook, "no-runbook", false, "skip writing agent runbook files (CLAUDE.md/AGENTS.md/GEMINI.md) into the workspace")
	cmd.Flags().StringVar(&sessionID, "session-id", "codex-session", "active session id")
	cmd.Flags().StringVar(&profile, "profile", "", "profile name")
	cmd.Flags().StringVar(&groupName, "group-name", "", "new WhatsApp group name; defaults to \"<Agent> Session\"")
	cmd.Flags().StringVar(&authorized, "authorized", "", "comma-separated phone numbers or WhatsApp JIDs allowed to control the session")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the manual setup guide instead of running the wizard")
	cmd.Flags().BoolVar(&yes, "yes", false, "accept prompts when all required values are provided by flags")
	cmd.Flags().BoolVar(&openQR, "open-qr", true, "open generated QR image with the system image viewer")
	cmd.Flags().StringVar(&qrImagePath, "qr-image", "", "path for generated QR PNG")
	cmd.Flags().BoolVar(&acceptSessionRisk, "accept-session-risk", false, "acknowledge unofficial transport and local session-storage risk without an interactive prompt")
	return cmd
}

func setupDocsURL() string {
	return "https://github.com/dnikolayev/coderoam/blob/main/docs/SETUP.md"
}

func setupNextLine() string {
	return "setup_next: run `coderoam setup` or read " + setupDocsURL()
}

type setupWizardOptions struct {
	Messenger         string
	Agent             string
	Workdir           string
	SessionID         string
	Profile           string
	GroupName         string
	Authorized        string
	Yes               bool
	OpenQR            bool
	QRImagePath       string
	AcceptSessionRisk bool
	NoRunbook         bool
}

type setupAuthorizedIdentity struct {
	Input     string
	Display   string
	SenderID  string
	InviteTo  string
	IsPhone   bool
	Confirmed bool
}

func (s *cliState) runSetupWizard(cmd *cobra.Command, opts setupWizardOptions) error {
	if opts.Messenger != "whatsapp" {
		return fmt.Errorf("%s setup is not implemented yet; choose --messenger whatsapp", opts.Messenger)
	}
	interactive := interactiveReader(cmd.InOrStdin())
	if !interactive && !opts.Yes {
		return fmt.Errorf("interactive setup requires a terminal; rerun with --print for commands or pass --yes with --authorized, --agent, and --workdir")
	}
	reader := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "coderoam setup")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "This creates a private WhatsApp group for continuing a local coding session from mobile.")
	fmt.Fprintln(out)

	cfg, path, err := config.LoadOrDefault(s.configPath)
	if err != nil {
		return err
	}
	if profile := strings.TrimSpace(opts.Profile); profile != "" {
		cfg.App.Profile = profile
	}
	if err := config.EnsureProfileDirs(cfg.App.Profile); err != nil {
		return err
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		if err := config.Save(path, cfg); err != nil {
			return err
		}
	}

	workdir := strings.TrimSpace(opts.Workdir)
	if workdir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workdir = cwd
		}
	}
	if workdir == "" {
		return fmt.Errorf("--workdir is required")
	}
	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID == "" {
		sessionID = "coderoam-session"
	}
	selected, err := setupSelectAgent(reader, out, opts.Agent, interactive, opts.Yes)
	if err != nil {
		return err
	}
	if selected.Key == "" {
		return fmt.Errorf("no agent selected")
	}
	groupName := strings.TrimSpace(opts.GroupName)
	if groupName == "" {
		groupName = selected.Display + " Session"
	}
	if len(groupName) > 25 {
		return fmt.Errorf("--group-name must be 25 characters or fewer")
	}
	runnerCfg, err := buildRunnerPreset(selected.Preset, workdir, 120, "", "", sessionID, "", nil, "")
	if err != nil {
		return err
	}
	cfg.Runner[selected.RunnerID] = runnerCfg

	if !opts.NoRunbook {
		if files, rbErr := agentRunbookFiles("all"); rbErr == nil {
			for _, name := range files {
				rbPath := filepath.Join(workdir, name)
				if _, werr := writeRunbookSection(rbPath, relayRunbook); werr != nil {
					fmt.Fprintf(out, "warning: could not write %s: %v\n", rbPath, werr)
				} else {
					fmt.Fprintf(out, "installed agent runbook: %s\n", rbPath)
				}
			}
		}
	}

	identities, err := setupCollectAuthorized(reader, out, opts.Authorized, interactive, opts.Yes)
	if err != nil {
		return err
	}
	cfg.Security.RequireSenderAllowlist = true
	for _, identity := range identities {
		cfg.Security.AllowedSenderIDs = appendUniqueString(cfg.Security.AllowedSenderIDs, identity.SenderID)
		cfg.Security.AdminSenderIDs = appendUniqueString(cfg.Security.AdminSenderIDs, identity.SenderID)
	}

	chatTransport, err := s.buildTransport(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	defer chatTransport.Close(context.Background())
	status, statusErr := chatTransport.Status(cmd.Context())
	if statusErr != nil {
		return statusErr
	}
	if strings.TrimSpace(status.Account) == "" {
		fmt.Fprintln(out, "WhatsApp login")
		if err := requireSessionRiskAcknowledgementWithReader(cmd, cfg.App.Profile, opts.AcceptSessionRisk, reader); err != nil {
			return err
		}
		if err := chatTransport.Login(cmd.Context(), types.LoginMethod{
			QR:          true,
			QRImagePath: opts.QRImagePath,
			OpenQRImage: opts.OpenQR,
		}); err != nil {
			return err
		}
		fmt.Fprintln(out, "WhatsApp linked.")
	} else {
		fmt.Fprintf(out, "WhatsApp linked: %s\n", logging.Redact(status.Account))
	}

	alias := defaultSessionAlias(groupName)
	if err := validateNewActiveSessionGroup(cfg, alias, sessionID); err != nil {
		return err
	}
	participants := setupInviteTargets(identities)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Creating WhatsApp group %q...\n", groupName)
	chat, err := chatTransport.CreateGroup(cmd.Context(), groupName, participants)
	if err != nil {
		return err
	}
	config.UpsertActiveSessionGroup(&cfg, config.GroupConfig{
		ID:              chat.ID,
		Alias:           alias,
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: sessionID,
		Enabled:         true,
		RelayManaged:    true,
	})
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	store, err := db.Open(config.ResolveDatabasePath(cfg))
	if err != nil {
		return err
	}
	defer store.Close()
	if _, err := store.MigrateMessagesToActiveInbox(cmd.Context(), cfg.App.Profile, chat.ID, alias, sessionID); err != nil {
		return err
	}
	inviteResults, err := sendActiveSessionInvites(cmd.Context(), chatTransport, cfg, chat.ID, groupName, participants)
	if err != nil {
		return err
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Ready.")
	fmt.Fprintf(out, "  messenger: WhatsApp\n")
	fmt.Fprintf(out, "  group: %s\n", groupName)
	fmt.Fprintf(out, "  agent: %s\n", selected.Display)
	fmt.Fprintf(out, "  session: %s\n", sessionID)
	fmt.Fprintf(out, "  authorized: %s\n", strings.Join(setupAuthorizedDisplays(identities), ", "))
	if len(inviteResults) > 0 {
		fmt.Fprintf(out, "  invites sent: %d\n", len(inviteResults))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Start the bridge:")
	fmt.Fprintln(out, "  coderoam run")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "If WhatsApp shows a new sender ID on first message, approve it only after confirming it is one of the authorized people:")
	fmt.Fprintln(out, "  coderoam senders allow <sender-id> --admin")
	return nil
}

func setupHowTo() string {
	return strings.TrimSpace(`coderoam needs a connected messenger before an agent session can continue from mobile.

WhatsApp is the implemented transport today. "telegram", "slack", and
"google-chat" are reserved transport names that report clear setup/status errors
until their adapters are implemented.

Quick WhatsApp setup:

  coderoam init
  coderoam auth login --profile bot --qr
  coderoam runners preset codex-code --id codex-code --workdir /path/to/workspace --yes
  coderoam runbook --workdir /path/to/workspace
  coderoam active start --name "Codex Session" --participants "+15550001111" --alias codex-session --session-id codex-session --yes
  coderoam run

For parallel clients, create one active group per client and never reuse session
ids. For example, use codex-session with codex-code and claude-session with
claude-code.

For scripted or CI login flows, add --accept-session-risk after reading
SECURITY.md. Interactive terminals will ask for acknowledgement instead.

In API-style agent sessions, drain the inbox at turn start with that group's
session id:

  coderoam inbox drain --format prompt --session-id <session-id>

Use a watcher only when the agent client continuously reads stdout while idle:

  coderoam inbox watch --format prompt --session-id <session-id>

For an existing WhatsApp group, use:

  coderoam chats list --groups
  coderoam active enable "<group-id>" --alias codex-session --session-id codex-session --managed

For screenshots, images, and other local media context, enable
transport.download_media = true in config; prompts will include local_path for
agent tooling.

Full setup guide:
  `) + "\n  " + setupDocsURL() + "\n"
}

type setupAgentCandidate struct {
	Key          string
	Display      string
	Command      string
	Preset       string
	RunnerID     string
	Instructions string
}

type setupAgentDetection struct {
	setupAgentCandidate
	Path  string
	Found bool
}

func setupAgentGuide(agent, workdir, sessionID string) string {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent == "" {
		agent = "auto"
	}
	if agent == "none" {
		return ""
	}
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workdir = cwd
		} else {
			workdir = "/path/to/workspace"
		}
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "codex-session"
	}
	detections, selected, selectedKnown := detectSetupAgents(agent)
	var b strings.Builder
	b.WriteString("\nDetected agent clients:\n\n")
	if !selectedKnown {
		fmt.Fprintf(&b, "  unknown --agent %q; supported values are auto, codex, claude, gemini, opencode, none\n\n", agent)
		selected = detections
	}
	foundAny := false
	for _, detection := range selected {
		status := "not found"
		if detection.Found {
			status = "found at " + detection.Path
			foundAny = true
		}
		fmt.Fprintf(&b, "  %s: %s\n", detection.Display, status)
		if detection.Found || agent != "auto" {
			detectionSessionID := setupGuideSessionID(agent, sessionID, detection.Key)
			fmt.Fprintf(&b, "    configure: coderoam runners preset %s --id %s --workdir %s --yes\n", detection.Preset, detection.RunnerID, shellQuote(workdir))
			fmt.Fprintf(&b, "    active group: coderoam active start --name %q --participants \"+15550001111\" --alias %s --session-id %s --yes\n", detection.Display+" Session", shellQuote(detectionSessionID), shellQuote(detectionSessionID))
			fmt.Fprintf(&b, "    instructions: %s\n", detection.Instructions)
		}
	}
	if agent == "auto" && !foundAny {
		b.WriteString("\n  No supported agent CLI was found in PATH. Install or log in to one of: codex, claude, gemini, opencode.\n")
		b.WriteString("  You can still print commands for a specific client with --agent codex, --agent claude, --agent gemini, or --agent opencode.\n")
	}
	b.WriteString("\n")
	return b.String()
}

func setupGuideSessionID(agent, requested, key string) string {
	requested = strings.TrimSpace(requested)
	key = strings.TrimSpace(key)
	if requested == "" {
		requested = "codex-session"
	}
	if strings.ToLower(strings.TrimSpace(agent)) != "auto" {
		return requested
	}
	switch requested {
	case "codex-session", "coderoam-session":
		if key != "" {
			return key + "-session"
		}
		return requested
	default:
		if key != "" {
			return requested + "-" + key
		}
		return requested
	}
}

func setupSelectAgent(reader *bufio.Reader, out io.Writer, agent string, interactive bool, yes bool) (setupAgentDetection, error) {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent == "" {
		agent = "auto"
	}
	if agent == "none" {
		return setupAgentDetection{}, fmt.Errorf("setup needs an agent client; use --print for manual setup without configuring one")
	}
	detections, selected, selectedKnown := detectSetupAgents(agent)
	if !selectedKnown {
		return setupAgentDetection{}, fmt.Errorf("unknown --agent %q; supported values are auto, codex, claude, gemini, opencode", agent)
	}
	if agent != "auto" {
		chosen := selected[0]
		if chosen.Found {
			fmt.Fprintf(out, "Agent: %s (%s)\n", chosen.Display, chosen.Path)
		} else {
			fmt.Fprintf(out, "Agent: %s (command not found yet; setup will still write the preset)\n", chosen.Display)
		}
		return chosen, nil
	}
	found := []setupAgentDetection{}
	for _, detection := range detections {
		if detection.Found {
			found = append(found, detection)
		}
	}
	if len(found) == 0 {
		return setupAgentDetection{}, fmt.Errorf("no supported agent CLI found on PATH; install or choose one with --agent codex, --agent claude, --agent gemini, or --agent opencode")
	}
	if len(found) == 1 || yes || !interactive {
		chosen := found[0]
		fmt.Fprintf(out, "Agent: %s (%s)\n", chosen.Display, chosen.Path)
		return chosen, nil
	}
	fmt.Fprintln(out, "Select agent:")
	for i, detection := range found {
		fmt.Fprintf(out, "  %d. %s (%s)\n", i+1, detection.Display, detection.Path)
	}
	for {
		fmt.Fprintf(out, "Choose 1-%d: ", len(found))
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return setupAgentDetection{}, err
		}
		index, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr == nil && index >= 1 && index <= len(found) {
			chosen := found[index-1]
			fmt.Fprintf(out, "Agent: %s\n", chosen.Display)
			return chosen, nil
		}
		if errors.Is(err, io.EOF) {
			return setupAgentDetection{}, fmt.Errorf("no agent selection received on stdin; rerun with --agent codex|claude|gemini|opencode, --print, or --yes")
		}
		fmt.Fprintln(out, "Enter one of the listed numbers.")
	}
}

func setupCollectAuthorized(reader *bufio.Reader, out io.Writer, value string, interactive bool, yes bool) ([]setupAuthorizedIdentity, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if !interactive {
			return nil, fmt.Errorf("--authorized is required")
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Who can control this coding session from WhatsApp?")
		fmt.Fprint(out, "Enter phone numbers, comma-separated: ")
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		value = strings.TrimSpace(line)
	}
	parts := splitCSV(value)
	if len(parts) == 0 {
		return nil, fmt.Errorf("at least one authorized phone number is required")
	}
	identities := make([]setupAuthorizedIdentity, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		identity, err := normalizeSetupAuthorizedIdentity(part)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(identity.SenderID)
		if seen[key] {
			continue
		}
		seen[key] = true
		identities = append(identities, identity)
	}
	if len(identities) == 0 {
		return nil, fmt.Errorf("at least one authorized phone number is required")
	}
	if yes {
		for i := range identities {
			identities[i].Confirmed = true
		}
		return identities, nil
	}
	if !interactive {
		return nil, fmt.Errorf("authorized numbers require confirmation; rerun interactively or pass --yes")
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Confirm authorized WhatsApp numbers before any invite is sent.")
	for i := range identities {
		fmt.Fprintf(out, "Type %s to confirm: ", identities[i].Display)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if strings.TrimSpace(line) != identities[i].Display {
			return nil, fmt.Errorf("confirmation did not match %s; no invites were sent", identities[i].Display)
		}
		identities[i].Confirmed = true
	}
	return identities, nil
}

func normalizeSetupAuthorizedIdentity(value string) (setupAuthorizedIdentity, error) {
	input := strings.TrimSpace(value)
	if input == "" {
		return setupAuthorizedIdentity{}, fmt.Errorf("authorized phone number is empty")
	}
	if strings.Contains(input, "@") {
		jid, err := whatsappweb.ParseChatID(input)
		if err != nil {
			return setupAuthorizedIdentity{}, fmt.Errorf("invalid authorized sender %q: %w", input, err)
		}
		normalized := jid.String()
		return setupAuthorizedIdentity{
			Input:    input,
			Display:  normalized,
			SenderID: normalized,
			InviteTo: normalized,
		}, nil
	}
	digits := normalizeSetupPhoneDigits(input)
	if len(digits) < 8 {
		return setupAuthorizedIdentity{}, fmt.Errorf("authorized phone number %q is too short", input)
	}
	display := "+" + digits
	return setupAuthorizedIdentity{
		Input:    input,
		Display:  display,
		SenderID: digits + "@s.whatsapp.net",
		InviteTo: display,
		IsPhone:  true,
	}, nil
}

func normalizeSetupPhoneDigits(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func setupInviteTargets(identities []setupAuthorizedIdentity) []string {
	out := make([]string, 0, len(identities))
	for _, identity := range identities {
		out = append(out, identity.InviteTo)
	}
	return out
}

func setupAuthorizedDisplays(identities []setupAuthorizedIdentity) []string {
	out := make([]string, 0, len(identities))
	for _, identity := range identities {
		out = append(out, identity.Display)
	}
	return out
}

func detectSetupAgents(agent string) ([]setupAgentDetection, []setupAgentDetection, bool) {
	candidates := []setupAgentCandidate{
		{Key: "codex", Display: "Codex", Command: "codex", Preset: "codex-code", RunnerID: "codex-code", Instructions: "docs/agents/codex.md"},
		{Key: "claude", Display: "Claude", Command: "claude", Preset: "claude-code", RunnerID: "claude-code", Instructions: "docs/agents/claude.md"},
		{Key: "gemini", Display: "Gemini", Command: "gemini", Preset: "gemini-code", RunnerID: "gemini-code", Instructions: "docs/agents/gemini.md"},
		{Key: "opencode", Display: "OpenCode", Command: "opencode", Preset: "opencode-code", RunnerID: "opencode-code", Instructions: "docs/agents/opencode.md"},
	}
	detections := make([]setupAgentDetection, 0, len(candidates))
	selected := []setupAgentDetection{}
	selectedKnown := agent == "auto"
	for _, candidate := range candidates {
		detection := setupAgentDetection{setupAgentCandidate: candidate}
		if path, err := commandLookPath(candidate.Command); err == nil {
			detection.Path = path
			detection.Found = true
		}
		detections = append(detections, detection)
		if agent == candidate.Key {
			selectedKnown = true
			selected = append(selected, detection)
		}
	}
	if agent == "auto" {
		selected = detections
	}
	return detections, selected, selectedKnown
}
