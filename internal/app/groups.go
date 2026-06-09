package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/logging"
	"github.com/dnikolayev/coderoam/internal/router"
	"github.com/dnikolayev/coderoam/internal/types"
)

func (s *cliState) sendersCommand() *cobra.Command {
	senders := &cobra.Command{Use: "senders", Short: "Manage authorized WhatsApp senders"}
	var admin bool
	allow := &cobra.Command{
		Use:   "allow <sender_id-or-phone>",
		Short: "Allow a WhatsApp sender to control active sessions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			identity, err := normalizeSetupAuthorizedIdentity(args[0])
			if err != nil {
				return err
			}
			cfg.Security.RequireSenderAllowlist = true
			cfg.Security.AllowedSenderIDs = appendUniqueString(cfg.Security.AllowedSenderIDs, identity.SenderID)
			if admin {
				cfg.Security.AdminSenderIDs = appendUniqueString(cfg.Security.AdminSenderIDs, identity.SenderID)
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			if admin {
				fmt.Printf("allowed sender=%s admin=true\n", logging.Redact(identity.SenderID))
			} else {
				fmt.Printf("allowed sender=%s\n", logging.Redact(identity.SenderID))
			}
			return nil
		},
	}
	allow.Flags().BoolVar(&admin, "admin", false, "also authorize WhatsApp slash/admin commands")
	senders.AddCommand(allow)
	return senders
}

func (s *cliState) chatsCommand() *cobra.Command {
	chats := &cobra.Command{Use: "chats", Short: "List and inspect chats"}
	var groupsOnly bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List available chats",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			chatTransport, err := s.buildTransport(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			items, err := chatTransport.ListChats(cmd.Context())
			if err != nil {
				return err
			}
			annotateAllowedChats(cfg, items)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CHAT_ID\tTYPE\tNAME\tPARTICIPANTS\tALLOWED\tALIAS")
			for _, item := range items {
				if groupsOnly && item.Type != types.ChatTypeGroup {
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%t\t%s\n", item.ID, item.Type, item.DisplayName, item.ParticipantCount, item.Allowed, item.Alias)
			}
			return w.Flush()
		},
	}
	list.Flags().BoolVar(&groupsOnly, "groups", false, "show groups only")

	search := &cobra.Command{
		Use:   "search <query>",
		Short: "Search chats by display name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			chatTransport, err := s.buildTransport(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			items, err := chatTransport.ListChats(cmd.Context())
			if err != nil {
				return err
			}
			query := strings.ToLower(args[0])
			for _, item := range items {
				if strings.Contains(strings.ToLower(item.DisplayName), query) || strings.Contains(item.ID, query) {
					fmt.Printf("%s\t%s\t%s\n", item.ID, item.Type, item.DisplayName)
				}
			}
			return nil
		},
	}

	inspect := &cobra.Command{
		Use:   "inspect <chat_id>",
		Short: "Inspect one chat",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			chatTransport, err := s.buildTransport(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			items, err := chatTransport.ListChats(cmd.Context())
			if err != nil {
				return err
			}
			annotateAllowedChats(cfg, items)
			for _, item := range items {
				if item.ID == args[0] {
					raw, _ := json.MarshalIndent(item, "", "  ")
					fmt.Println(string(raw))
					return nil
				}
			}
			return fmt.Errorf("chat not found: %s", args[0])
		},
	}
	chats.AddCommand(list, search, inspect)
	return chats
}

func (s *cliState) groupsCommand() *cobra.Command {
	groups := &cobra.Command{Use: "groups", Short: "Manage allowlisted WhatsApp groups"}
	var alias string
	var runnerID string
	allow := &cobra.Command{
		Use:   "allow <chat_id>",
		Short: "Allow one group chat",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			if runnerID == "" {
				runnerID = "default"
			}
			config.UpsertGroup(&cfg, config.GroupConfig{ID: args[0], Alias: alias, Runner: runnerID, Mode: config.GroupModeRunner, Enabled: true})
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			fmt.Printf("allowed group=%s alias=%s runner=%s\n", args[0], alias, runnerID)
			return nil
		},
	}
	allow.Flags().StringVar(&alias, "alias", "", "local group alias")
	allow.Flags().StringVar(&runnerID, "runner", "default", "runner id")

	deny := &cobra.Command{
		Use:   "deny <chat_id>",
		Short: "Disable one group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			if !config.DenyGroup(&cfg, args[0]) {
				return fmt.Errorf("group not configured: %s", args[0])
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			fmt.Printf("denied group=%s\n", args[0])
			return nil
		},
	}

	setRunner := &cobra.Command{
		Use:   "set-runner <chat_id> <runner_id>",
		Short: "Change the runner used by an allowlisted group",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			runnerID := args[1]
			if _, ok := cfg.Runner[runnerID]; !ok {
				return fmt.Errorf("runner not configured: %s", runnerID)
			}
			for i := range cfg.Groups {
				if cfg.Groups[i].ID == args[0] {
					cfg.Groups[i].Runner = runnerID
					if cfg.Groups[i].Mode == "" {
						cfg.Groups[i].Mode = config.GroupModeRunner
					}
					cfg.Groups[i].Enabled = true
					if err := config.Save(path, cfg); err != nil {
						return err
					}
					fmt.Printf("group=%s runner=%s\n", args[0], runnerID)
					return nil
				}
			}
			return fmt.Errorf("group not configured: %s", args[0])
		},
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List configured groups",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CHAT_ID\tALIAS\tMODE\tRUNNER\tENABLED")
			for _, group := range cfg.Groups {
				mode := group.Mode
				if mode == "" {
					mode = config.GroupModeRunner
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%t\n", group.ID, group.Alias, mode, group.Runner, group.Enabled)
			}
			return w.Flush()
		},
	}
	var createName string
	var createParticipants string
	var createAlias string
	var createRunner string
	var createYes bool
	var createAllow bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a small WhatsApp group from the local terminal",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !createYes {
				return fmt.Errorf("refusing to create a WhatsApp group without --yes")
			}
			if createName == "" {
				return fmt.Errorf("--name is required")
			}
			participants := splitCSV(createParticipants)
			if len(participants) == 0 {
				return fmt.Errorf("--participants is required")
			}
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			chatTransport, err := s.buildTransport(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			chat, err := chatTransport.CreateGroup(cmd.Context(), createName, participants)
			if err != nil {
				return err
			}
			fmt.Printf("created group id=%s name=%q participants=%d\n", chat.ID, chat.DisplayName, chat.ParticipantCount)
			if createAllow {
				if createRunner == "" {
					createRunner = "default"
				}
				if createAlias == "" {
					createAlias = createName
				}
				config.UpsertGroup(&cfg, config.GroupConfig{
					ID:      chat.ID,
					Alias:   createAlias,
					Runner:  createRunner,
					Mode:    config.GroupModeRunner,
					Enabled: true,
				})
				if err := config.Save(path, cfg); err != nil {
					return err
				}
				fmt.Printf("allowed group=%s alias=%s runner=%s\n", chat.ID, createAlias, createRunner)
			}
			return nil
		},
	}
	create.Flags().StringVar(&createName, "name", "", "new group name, 25 characters max")
	create.Flags().StringVar(&createParticipants, "participants", "", "comma-separated phone numbers or WhatsApp JIDs")
	create.Flags().StringVar(&createAlias, "alias", "", "local alias if allowlisting")
	create.Flags().StringVar(&createRunner, "runner", "default", "runner id if allowlisting")
	create.Flags().BoolVar(&createAllow, "allow", true, "allowlist the created group")
	create.Flags().BoolVar(&createYes, "yes", false, "confirm creating a WhatsApp group")
	var inviteReset bool
	inviteLink := &cobra.Command{
		Use:   "invite-link <chat_id>",
		Short: "Print a WhatsApp group invite link",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			chatTransport, err := s.buildTransport(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			link, err := chatTransport.GetGroupInviteLink(cmd.Context(), args[0], inviteReset)
			if err != nil {
				return err
			}
			fmt.Println(link)
			return nil
		},
	}
	inviteLink.Flags().BoolVar(&inviteReset, "reset", false, "revoke the old invite link and generate a new one")

	var inviteTo string
	var sendInviteReset bool
	sendInvite := &cobra.Command{
		Use:   "send-invite <chat_id>",
		Short: "Send a group invite link to a phone number or WhatsApp JID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inviteTo == "" {
				return fmt.Errorf("--to is required")
			}
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			chatTransport, err := s.buildTransport(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			link, err := chatTransport.GetGroupInviteLink(cmd.Context(), args[0], sendInviteReset)
			if err != nil {
				return err
			}
			text := fmt.Sprintf("Join the Codex Bridge WhatsApp group: %s", link)
			sent, err := chatTransport.SendText(cmd.Context(), inviteTo, text, types.SendOptions{TypingIndicator: cfg.Reply.TypingIndicator})
			if err != nil {
				return err
			}
			fmt.Printf("sent invite id=%s to=%s\n", sent.ID, logging.Redact(inviteTo))
			return nil
		},
	}
	sendInvite.Flags().StringVar(&inviteTo, "to", "", "recipient phone number or WhatsApp JID")
	sendInvite.Flags().BoolVar(&sendInviteReset, "reset", false, "revoke the old invite link and generate a new one")
	groups.AddCommand(allow, deny, setRunner, list, create, inviteLink, sendInvite)
	return groups
}

func (s *cliState) approvalsCommand() *cobra.Command {
	approvals := &cobra.Command{Use: "approvals", Short: "Review and answer pending runner approvals"}
	var status string
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List pending approval/input requests",
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
			records, err := store.ListPendingInteractions(cmd.Context(), cfg.App.Profile, status, limit)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tCHAT\tSENDER\tRUNNER\tSTATUS\tEXPIRES\tPROMPT")
			for _, record := range records {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
					record.ID, logging.Redact(record.ChatID), logging.Redact(record.SenderID), record.RunnerID,
					record.Status, record.ExpiresAt.Format(time.RFC3339), oneLine(record.Prompt, 100))
			}
			return w.Flush()
		},
	}
	list.Flags().StringVar(&status, "status", "pending", "status filter; empty for all")
	list.Flags().IntVar(&limit, "limit", 20, "maximum rows")

	show := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one approval/input request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return err
			}
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			record, ok, err := store.GetPendingInteraction(cmd.Context(), cfg.App.Profile, id)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("approval not found: %d", id)
			}
			raw, _ := json.MarshalIndent(record, "", "  ")
			fmt.Println(string(raw))
			return nil
		},
	}

	var answerText string
	approve := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve one pending runner request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.answerApproval(cmd.Context(), args[0], nonEmpty(answerText, "approved"))
		},
	}
	approve.Flags().StringVar(&answerText, "text", "", "approval text sent back to the runner")

	var rejectText string
	reject := &cobra.Command{
		Use:   "reject <id>",
		Short: "Reject one pending runner request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.answerApproval(cmd.Context(), args[0], nonEmpty(rejectText, "rejected"))
		},
	}
	reject.Flags().StringVar(&rejectText, "text", "", "rejection text sent back to the runner")

	approvals.AddCommand(list, show, approve, reject)
	return approvals
}

func (s *cliState) answerApproval(ctx context.Context, idText, answer string) error {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return fmt.Errorf("approval answer text is required")
	}
	cfg, _, err := s.loadConfig()
	if err != nil {
		return err
	}
	store, err := db.Open(config.ResolveDatabasePath(cfg))
	if err != nil {
		return err
	}
	defer store.Close()
	record, ok, err := store.GetPendingInteraction(ctx, cfg.App.Profile, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("approval not found: %d", id)
	}
	if record.Status != "pending" {
		return fmt.Errorf("approval %d is %s", id, record.Status)
	}
	if time.Now().After(record.ExpiresAt) {
		_ = store.ExpirePendingInteractions(ctx, cfg.App.Profile)
		return fmt.Errorf("approval %d has expired", id)
	}
	chatTransport, err := s.buildTransport(ctx, cfg)
	if err != nil {
		return err
	}
	defer chatTransport.Close(context.Background())
	bridgeRouter := router.New(cfg, store, chatTransport)
	defer bridgeRouter.Stop(context.Background())
	group, _ := config.FindGroup(cfg, record.ChatID)
	result := bridgeRouter.Handle(ctx, types.IncomingMessage{
		ID:        fmt.Sprintf("local-approval-%d-%d", id, time.Now().UnixNano()),
		ChatID:    record.ChatID,
		ChatType:  types.ChatTypeGroup,
		ChatName:  group.Alias,
		SenderID:  record.SenderID,
		Text:      answer,
		RawText:   answer,
		Timestamp: time.Now(),
	})
	fmt.Printf("approval %d answered: %s\n", id, result.Reason)
	if result.Ignored {
		return fmt.Errorf("approval answer was ignored: %s", result.Reason)
	}
	return nil
}

func annotateAllowedChats(cfg config.Config, items []types.Chat) {
	for i := range items {
		if group, ok := config.FindGroup(cfg, items[i].ID); ok {
			items[i].Allowed = true
			items[i].Alias = group.Alias
		}
	}
}
