package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/logging"
	"github.com/dnikolayev/coderoam/internal/router"
	"github.com/dnikolayev/coderoam/internal/transport/fake"
	"github.com/dnikolayev/coderoam/internal/types"
)

func (s *cliState) sendCommand() *cobra.Command {
	var to string
	var text string
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send one WhatsApp text message from the linked account",
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return fmt.Errorf("--to is required")
			}
			if text == "" {
				return fmt.Errorf("--text is required")
			}
			cfg, _, err := s.loadConfig()
			if err != nil {
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
			if err := chatTransport.Connect(cmd.Context()); err != nil {
				return err
			}
			sent, err := chatTransport.SendText(cmd.Context(), to, text, types.SendOptions{TypingIndicator: cfg.Reply.TypingIndicator})
			if err != nil {
				return err
			}
			fmt.Printf("sent id=%s chat=%s\n", sent.ID, logging.Redact(sent.ChatID))
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "WhatsApp JID, group JID, or phone number")
	cmd.Flags().StringVar(&text, "text", "", "message text")
	return cmd
}

func (s *cliState) notifyCommand() *cobra.Command {
	var chat string
	var text string
	var important bool
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Queue an active-session WhatsApp notification",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			if strings.TrimSpace(chat) == "" {
				return fmt.Errorf("--chat is required")
			}
			if strings.TrimSpace(text) == "" {
				body, _ := io.ReadAll(os.Stdin)
				text = string(body)
			}
			text = strings.TrimSpace(text)
			if text == "" {
				return fmt.Errorf("--text or stdin text is required")
			}
			group, ok := resolveGroup(cfg, chat)
			if !ok {
				return fmt.Errorf("chat not configured: %s", chat)
			}
			if !group.Enabled || group.Mode != config.GroupModeActiveSession {
				return fmt.Errorf("chat %s is not an enabled active-session group", chat)
			}
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			id, err := store.QueueActiveOutbox(cmd.Context(), cfg.App.Profile, group.ID, text, important)
			if err != nil {
				return err
			}
			fmt.Printf("queued active notification id=%d chat=%s important=%t\n", id, group.Alias, important)
			return nil
		},
	}
	cmd.Flags().StringVar(&chat, "chat", "", "active group alias or chat id")
	cmd.Flags().StringVar(&text, "text", "", "notification text; stdin is used when empty")
	cmd.Flags().BoolVar(&important, "important", true, "mark this notification as important")
	return cmd
}

func (s *cliState) testRouteCommand() *cobra.Command {
	var chatID string
	var senderID string
	var text string
	cmd := &cobra.Command{
		Use:   "test-route",
		Short: "Run one fake incoming message through the bridge router",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			if chatID == "" || senderID == "" {
				return fmt.Errorf("--chat and --sender are required")
			}
			if _, ok := config.FindGroup(cfg, chatID); !ok {
				config.UpsertGroup(&cfg, config.GroupConfig{ID: chatID, Alias: "fake", Runner: "default", Enabled: true})
			}
			if err := config.EnsureProfileDirs(cfg.App.Profile); err != nil {
				return err
			}
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			fakeTransport := fake.New([]types.Chat{{ID: chatID, Type: types.ChatTypeGroup, DisplayName: "fake"}})
			bridgeRouter := router.New(cfg, store, fakeTransport)
			defer bridgeRouter.Stop(context.Background())
			result := bridgeRouter.Handle(cmd.Context(), types.IncomingMessage{
				ID:         "fake-" + strconv.FormatInt(time.Now().UnixNano(), 10),
				ChatID:     chatID,
				ChatType:   types.ChatTypeGroup,
				ChatName:   "fake",
				SenderID:   senderID,
				SenderName: "local",
				Text:       strings.TrimPrefix(text, cfg.Trigger.Prefix),
				RawText:    text,
				Timestamp:  time.Now(),
			})
			raw, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(raw))
			return nil
		},
	}
	cmd.Flags().StringVar(&chatID, "chat", "", "fake chat id")
	cmd.Flags().StringVar(&senderID, "sender", "", "fake sender id")
	cmd.Flags().StringVar(&text, "text", "@bridge hello", "incoming message text")
	return cmd
}
