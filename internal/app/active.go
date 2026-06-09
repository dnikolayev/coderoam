package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/logging"
	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/types"
)

func (s *cliState) activeCommand() *cobra.Command {
	active := &cobra.Command{Use: "active", Short: "Manage active Codex session relay groups"}
	var startName string
	var startParticipants string
	var startAlias string
	var startSessionID string
	var startRunner string
	var startInviteTo string
	var startYes bool
	start := &cobra.Command{
		Use:   "start",
		Short: "Create a new WhatsApp group routed to an active session inbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !startYes {
				return fmt.Errorf("refusing to create a WhatsApp group without --yes")
			}
			startName = strings.TrimSpace(startName)
			if startName == "" {
				return fmt.Errorf("--name is required")
			}
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			participants := splitCSV(startParticipants)
			if len(participants) == 0 {
				// Default to the already-authorized owner(s) so a new lane can be
				// created without re-asking for a number coderoam already knows.
				participants = activeDefaultParticipants(cfg)
				if len(participants) == 0 {
					return fmt.Errorf("--participants is required (no admin/allowed sender configured to default to)")
				}
				fmt.Printf("no --participants given; inviting configured owner(s): %s\n", strings.Join(participants, ", "))
			}
			startRunner = strings.TrimSpace(startRunner)
			if startRunner != "" {
				if _, ok := cfg.Runner[startRunner]; !ok {
					return fmt.Errorf("runner not configured: %s", startRunner)
				}
			}
			startAlias = strings.TrimSpace(startAlias)
			if startAlias == "" {
				startAlias = defaultSessionAlias(startName)
			}
			startSessionID = strings.TrimSpace(startSessionID)
			if startSessionID == "" {
				startSessionID = startAlias
			}
			if err := validateNewActiveSessionGroup(cfg, startAlias, startSessionID); err != nil {
				return err
			}
			if err := config.EnsureProfileDirs(cfg.App.Profile); err != nil {
				return err
			}
			chatTransport, err := s.buildTransport(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			chat, err := chatTransport.CreateGroup(cmd.Context(), startName, participants)
			if err != nil {
				return err
			}
			config.UpsertActiveSessionGroup(&cfg, config.GroupConfig{
				ID:              chat.ID,
				Alias:           startAlias,
				Runner:          startRunner,
				Mode:            config.GroupModeActiveSession,
				ActiveSessionID: startSessionID,
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
			migrated, err := store.MigrateMessagesToActiveInbox(cmd.Context(), cfg.App.Profile, chat.ID, startAlias, startSessionID)
			if err != nil {
				return err
			}
			inviteRecipients := activeStartInviteRecipients(participants, startInviteTo)
			inviteResults, err := sendActiveSessionInvites(cmd.Context(), chatTransport, cfg, chat.ID, startName, inviteRecipients)
			if err != nil {
				return err
			}
			fmt.Printf("created active group id=%s name=%q participants=%d\n", chat.ID, chat.DisplayName, chat.ParticipantCount)
			fmt.Printf("active group=%s alias=%s session=%s runner=%s managed=true migrated=%d\n", chat.ID, startAlias, startSessionID, nonEmpty(startRunner, "-"), migrated)
			for _, result := range inviteResults {
				fmt.Printf("sent invite id=%s to=%s\n", result.ID, logging.Redact(result.Recipient))
			}
			fmt.Printf("watch: coderoam inbox watch --format prompt --session-id %s\n", shellQuote(startSessionID))
			fmt.Printf("notify: coderoam notify --chat %s --important --text %s\n", shellQuote(startAlias), shellQuote("Update..."))
			return nil
		},
	}
	start.Flags().StringVar(&startName, "name", "", "new group name, 25 characters max")
	start.Flags().StringVar(&startParticipants, "participants", "", "comma-separated phone numbers or WhatsApp JIDs")
	start.Flags().StringVar(&startAlias, "alias", "", "local active group alias; defaults to a slug of --name")
	start.Flags().StringVar(&startSessionID, "session-id", "", "active session id; defaults to --alias")
	start.Flags().StringVar(&startRunner, "runner", "", "optional fallback runner id; blank keeps messages queued for a live watcher")
	start.Flags().StringVar(&startInviteTo, "invite-to", "", "comma-separated phone numbers or WhatsApp JIDs to DM the group invite link; defaults to --participants")
	start.Flags().BoolVar(&startYes, "yes", false, "confirm creating a WhatsApp group")

	var alias string
	var sessionID string
	var enableManaged bool
	enable := &cobra.Command{
		Use:   "enable <chat_id>",
		Short: "Route one allowlisted group into the active session inbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			var existing config.GroupConfig
			for _, group := range cfg.Groups {
				if group.ID == args[0] {
					existing = group
					break
				}
			}
			if existing.ID != "" && existing.Mode == config.GroupModeActiveSession && existing.RelayManaged && existing.Archived && !existing.Enabled {
				return fmt.Errorf("archived relay-managed group %s cannot be re-enabled; use active start to create a fresh group for this session", args[0])
			}
			if alias == "" {
				alias = nonEmpty(existing.Alias, args[0])
			}
			if sessionID == "" {
				sessionID = nonEmpty(config.ActiveSessionID(existing), alias)
			}
			config.UpsertGroup(&cfg, config.GroupConfig{
				ID:              args[0],
				Alias:           alias,
				Runner:          existing.Runner,
				Mode:            config.GroupModeActiveSession,
				ActiveSessionID: sessionID,
				RelayManaged:    existing.RelayManaged || enableManaged,
				Enabled:         true,
			})
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			migrated, err := store.MigrateMessagesToActiveInbox(cmd.Context(), cfg.App.Profile, args[0], alias, sessionID)
			if err != nil {
				return err
			}
			fmt.Printf("active group=%s alias=%s session=%s runner=%s managed=%t migrated=%d\n", args[0], alias, sessionID, nonEmpty(existing.Runner, "-"), existing.RelayManaged || enableManaged, migrated)
			return nil
		},
	}
	enable.Flags().StringVar(&alias, "alias", "", "local group alias")
	enable.Flags().StringVar(&sessionID, "session-id", "", "active Codex session id")
	enable.Flags().BoolVar(&enableManaged, "managed", false, "adopt this existing dedicated group into relay-managed auto-archive lifecycle")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show active relay inbox/outbox status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			if _, err := store.ExpireActiveWatchers(cmd.Context(), cfg.App.Profile, activeWatcherStatusStaleAfter); err != nil {
				return err
			}
			counts, err := store.ActiveInboxCounts(cmd.Context(), cfg.App.Profile)
			if err != nil {
				return err
			}
			outboxPending, err := store.ActiveOutboxPendingCount(cmd.Context(), cfg.App.Profile)
			if err != nil {
				return err
			}
			readReceiptsPending, err := store.ActiveReadReceiptPendingCount(cmd.Context(), cfg.App.Profile)
			if err != nil {
				return err
			}
			watchers, err := store.ListActiveWatchers(cmd.Context(), cfg.App.Profile)
			if err != nil {
				return err
			}
			watchersBySession := map[string]db.ActiveWatcherRecord{}
			for _, watcher := range watchers {
				watchersBySession[watcher.SessionID] = watcher
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CHAT_ID\tALIAS\tSESSION\tMODE\tRUNNER\tENABLED\tMANAGED\tARCHIVED\tWATCHER\tHEARTBEAT")
			for _, group := range cfg.Groups {
				if group.Mode == config.GroupModeActiveSession {
					sessionID := config.ActiveSessionID(group)
					watcherLabel := "none"
					heartbeat := "-"
					if watcher, ok := watchersBySession[sessionID]; ok {
						watcherLabel = fmt.Sprintf("%s/%s", watcher.Status, watcher.ConsumerID)
						heartbeat = watcher.HeartbeatAt.Format(time.RFC3339)
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%t\t%t\t%t\t%s\t%s\n", group.ID, group.Alias, sessionID, group.Mode, group.Runner, group.Enabled, group.RelayManaged, group.Archived, watcherLabel, heartbeat)
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			fmt.Printf("inbox_unread: %d\n", counts["unread"])
			fmt.Printf("inbox_claimed: %d\n", counts["claimed"])
			fmt.Printf("outbox_pending: %d\n", outboxPending)
			fmt.Printf("read_receipts_pending: %d\n", readReceiptsPending)
			return nil
		},
	}
	active.AddCommand(start, enable, status)
	return active
}

type activeInviteResult struct {
	Recipient string
	ID        string
}

// activeDefaultParticipants returns the configured owner identities (admin
// senders first, then allowed senders, deduped) to invite when active start is
// run without explicit --participants, so an agent can create a new lane
// without re-asking for a number coderoam already knows.
func activeDefaultParticipants(cfg config.Config) []string {
	seen := map[string]bool{}
	out := []string{}
	combined := append(append([]string{}, cfg.Security.AdminSenderIDs...), cfg.Security.AllowedSenderIDs...)
	for _, id := range combined {
		id = strings.TrimSpace(id)
		if !activeDefaultParticipantAllowed(id) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func activeDefaultParticipantAllowed(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	lower := strings.ToLower(id)
	if strings.HasSuffix(lower, "@lid") || strings.HasSuffix(lower, "@g.us") {
		return false
	}
	return true
}

func activeStartInviteRecipients(participants []string, inviteTo string) []string {
	recipients := splitCSV(inviteTo)
	if len(recipients) == 0 {
		recipients = participants
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		key := strings.ToLower(strings.TrimSpace(recipient))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, recipient)
	}
	return out
}

func sendActiveSessionInvites(ctx context.Context, chatTransport transport.ChatTransport, cfg config.Config, chatID, groupName string, recipients []string) ([]activeInviteResult, error) {
	if len(recipients) == 0 {
		return nil, nil
	}
	link, err := chatTransport.GetGroupInviteLink(ctx, chatID, false)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Join the coderoam active session group %q: %s\n\nOpen this WhatsApp link to enter the session chat.", groupName, link)
	results := make([]activeInviteResult, 0, len(recipients))
	for _, recipient := range recipients {
		sent, err := chatTransport.SendText(ctx, recipient, text, types.SendOptions{TypingIndicator: cfg.Reply.TypingIndicator})
		if err != nil {
			return results, fmt.Errorf("send invite to %s: %w", logging.Redact(recipient), err)
		}
		results = append(results, activeInviteResult{Recipient: recipient, ID: sent.ID})
	}
	return results, nil
}

func defaultSessionAlias(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	alias := strings.Trim(b.String(), "-")
	if alias == "" {
		return "session"
	}
	return alias
}

func validateNewActiveSessionGroup(cfg config.Config, alias string, sessionID string) error {
	for _, group := range cfg.Groups {
		if group.Mode == config.GroupModeActiveSession && group.RelayManaged && group.Archived && !group.Enabled {
			continue
		}
		if group.Alias == alias {
			return fmt.Errorf("group alias already configured: %s", alias)
		}
		if group.Mode == config.GroupModeActiveSession && config.ActiveSessionID(group) == sessionID {
			return fmt.Errorf("active session id already configured: %s", sessionID)
		}
	}
	return nil
}
