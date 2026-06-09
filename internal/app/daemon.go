package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/logging"
	"github.com/dnikolayev/coderoam/internal/router"
	"github.com/dnikolayev/coderoam/internal/transport"
	"github.com/dnikolayev/coderoam/internal/types"
)

func (s *cliState) runCommand() *cobra.Command {
	var profile string
	var takeover bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the local WhatsApp bridge daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			cfg, path, err := s.loadConfig()
			if err != nil {
				return err
			}
			if profile != "" {
				cfg.App.Profile = profile
			}
			if err := config.EnsureProfileDirs(cfg.App.Profile); err != nil {
				return err
			}
			releaseLock, err := acquireRunLock(cfg.App.Profile, takeover)
			if err != nil {
				return err
			}
			defer releaseLock()
			store, err := db.Open(config.ResolveDatabasePath(cfg))
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.EnsureProfile(ctx, cfg.App.Profile); err != nil {
				return err
			}
			recovered, err := store.RecoverAbandonedActiveInbox(ctx, cfg.App.Profile, "", activeInboxClaimStaleAfter)
			if err != nil {
				return err
			}
			if recovered > 0 {
				fmt.Printf("[active-inbox] recovered_claims=%d\n", recovered)
			}
			chatTransport, err := s.buildTransport(ctx, cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			bridgeRouter := router.New(cfg, store, chatTransport)
			defer bridgeRouter.Stop(context.Background())
			queueDepth := cfg.Concurrency.QueueMaxDepthPerGroup
			if queueDepth <= 0 {
				queueDepth = 50
			}
			messages := make(chan types.IncomingMessage, queueDepth)
			groupEvents := make(chan types.GroupEvent, queueDepth)
			var workers sync.WaitGroup
			workers.Add(1)
			go func() {
				defer workers.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case msg := <-messages:
						result := bridgeRouter.Handle(ctx, msg)
						if result.Ignored {
							fmt.Printf("[ignored] chat=%s reason=%s\n", logging.Redact(msg.ChatID), result.Reason)
							continue
						}
						fmt.Printf("[processed] chat=%s replies=%d\n", logging.Redact(msg.ChatID), len(result.Sent))
					}
				}
			}()
			workers.Add(1)
			go func() {
				defer workers.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case event := <-groupEvents:
						updated, archived, err := handleRelayGroupLifecycleEvent(ctx, cfg, path, store, chatTransport, event)
						if err != nil {
							fmt.Printf("[group-event] chat=%s error=%s\n", logging.Redact(event.ChatID), err)
							continue
						}
						if archived {
							cfg = updated
							bridgeRouter.SetConfig(updated)
							fmt.Printf("[group-event] chat=%s archived=true\n", logging.Redact(event.ChatID))
						}
					}
				}
			}()
			workers.Add(1)
			go func() {
				defer workers.Done()
				ticker := time.NewTicker(2 * time.Second)
				defer ticker.Stop()
				for {
					receipts, err := sendPendingActiveReadReceipts(ctx, store, chatTransport, cfg, 20)
					if err != nil {
						fmt.Printf("[active-read] error=%s\n", err)
					} else if receipts > 0 {
						fmt.Printf("[active-read] sent=%d\n", receipts)
					}
					sent, err := sendPendingActiveOutbox(ctx, store, chatTransport, cfg, 20)
					if err != nil {
						fmt.Printf("[active-outbox] error=%s\n", err)
					} else if sent > 0 {
						fmt.Printf("[active-outbox] sent=%d\n", sent)
					}
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
				}
			}()
			chatTransport.Subscribe(func(eventCtx context.Context, msg types.IncomingMessage) {
				select {
				case <-eventCtx.Done():
					return
				case <-ctx.Done():
					return
				case messages <- msg:
					fmt.Printf("[queued] chat=%s\n", logging.Redact(msg.ChatID))
				default:
					fmt.Printf("[ignored] chat=%s reason=message queue full\n", logging.Redact(msg.ChatID))
				}
			})
			chatTransport.SubscribeGroupEvents(func(eventCtx context.Context, event types.GroupEvent) {
				select {
				case <-eventCtx.Done():
					return
				case <-ctx.Done():
					return
				case groupEvents <- event:
					fmt.Printf("[group-event] chat=%s left=%d participants=%d deleted=%t\n", logging.Redact(event.ChatID), len(event.LeftParticipantIDs), event.ParticipantCount, event.Deleted)
				default:
					fmt.Printf("[ignored] chat=%s reason=group event queue full\n", logging.Redact(event.ChatID))
				}
			})
			if err := chatTransport.Connect(ctx); err != nil {
				return err
			}
			status, _ := chatTransport.Status(ctx)
			fmt.Printf("[connected] profile=%s account=%q %s\n", cfg.App.Profile, logging.Redact(status.Account), status.Detail)
			fmt.Printf("[watching] %d allowed groups\n", len(enabledGroups(cfg.Groups)))
			for _, group := range enabledGroups(cfg.Groups) {
				if group.Mode != config.GroupModeActiveSession {
					continue
				}
				migrated, err := store.MigrateMessagesToActiveInbox(ctx, cfg.App.Profile, group.ID, group.Alias, config.ActiveSessionID(group))
				if err != nil {
					return err
				}
				if migrated > 0 {
					fmt.Printf("[active-inbox] migrated=%d chat=%s\n", migrated, logging.Redact(group.ID))
				}
			}
			pending, err := store.PendingIncomingMessages(ctx, cfg.App.Profile, queueDepth)
			if err != nil {
				return err
			}
			for _, msg := range pending {
				if group, ok := config.FindGroup(cfg, msg.ChatID); ok && group.Mode == config.GroupModeActiveSession {
					continue
				}
				select {
				case <-ctx.Done():
					return nil
				case messages <- msg:
					fmt.Printf("[queued] chat=%s reason=pending\n", logging.Redact(msg.ChatID))
				default:
					fmt.Printf("[ignored] chat=%s reason=pending queue full\n", logging.Redact(msg.ChatID))
				}
			}
			fmt.Println("[ready] press Ctrl-C to stop")
			<-ctx.Done()
			fmt.Println("[shutdown] stopping bridge")
			workers.Wait()
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "profile name")
	cmd.Flags().BoolVar(&takeover, "takeover", false, "take over the messenger connection from an already-running daemon (stops the incumbent)")
	return cmd
}

func sendPendingActiveOutbox(ctx context.Context, store *db.Store, chatTransport transport.ChatTransport, cfg config.Config, limit int) (int, error) {
	records, err := store.PendingActiveOutbox(ctx, cfg.App.Profile, limit)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, record := range records {
		if _, err := chatTransport.SendText(ctx, record.ChatID, record.Text, types.SendOptions{TypingIndicator: cfg.Reply.TypingIndicator}); err != nil {
			if markErr := store.MarkActiveOutboxFailed(ctx, record.ID, err); markErr != nil {
				return sent, markErr
			}
			continue
		}
		if err := store.MarkActiveOutboxSent(ctx, record.ID); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

func sendPendingActiveReadReceipts(ctx context.Context, store *db.Store, chatTransport transport.ChatTransport, cfg config.Config, limit int) (int, error) {
	records, err := store.PendingActiveReadReceipts(ctx, cfg.App.Profile, limit)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, record := range records {
		msg := types.IncomingMessage{
			ID:       record.ExternalMessageID,
			ChatID:   record.ChatID,
			SenderID: record.SenderID,
		}
		if err := chatTransport.MarkRead(ctx, msg); err != nil {
			if markErr := store.MarkActiveReadReceiptFailed(ctx, record.ID, err); markErr != nil {
				return sent, markErr
			}
			continue
		}
		if err := store.MarkActiveReadReceiptSent(ctx, record.ID); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

func handleRelayGroupLifecycleEvent(ctx context.Context, cfg config.Config, configPath string, store *db.Store, chatTransport transport.ChatTransport, event types.GroupEvent) (config.Config, bool, error) {
	groupIndex := -1
	for i, group := range cfg.Groups {
		if group.ID == event.ChatID && group.Mode == config.GroupModeActiveSession && group.RelayManaged && group.Enabled && !group.Archived {
			groupIndex = i
			break
		}
	}
	if groupIndex < 0 {
		return cfg, false, nil
	}
	archive, reason := shouldArchiveRelayGroup(event)
	if !archive {
		return cfg, false, nil
	}
	group := cfg.Groups[groupIndex]
	archiveErrText := ""
	if chatTransport != nil {
		if err := chatTransport.ArchiveChat(ctx, group.ID); err != nil {
			archiveErrText = err.Error()
		}
	}
	deletedRows, err := store.DeleteChatData(ctx, cfg.App.Profile, group.ID)
	if err != nil {
		return cfg, false, err
	}
	group.Enabled = false
	group.Archived = true
	group.ArchivedAt = time.Now().UTC().Format(time.RFC3339Nano)
	group.ArchiveReason = reason
	cfg.Groups[groupIndex] = group
	if err := config.Save(configPath, cfg); err != nil {
		return cfg, false, err
	}
	_ = store.Audit(ctx, cfg.App.Profile, "relay_group_archived", event.SenderID, group.ID, map[string]any{
		"alias":                group.Alias,
		"session_id":           config.ActiveSessionID(group),
		"reason":               reason,
		"left_participant_ids": event.LeftParticipantIDs,
		"participant_count":    event.ParticipantCount,
		"deleted":              event.Deleted,
		"deleted_local_rows":   deletedRows,
		"device_archive_error": archiveErrText,
		"reactivation_command": fmt.Sprintf("coderoam active start --name %q --alias %s --session-id %s --participants <owner> --yes", group.Alias, group.Alias, config.ActiveSessionID(group)),
	})
	return cfg, true, nil
}

func shouldArchiveRelayGroup(event types.GroupEvent) (bool, string) {
	if event.Deleted {
		return true, "group deleted"
	}
	if len(event.LeftParticipantIDs) > 0 {
		return true, "participant left"
	}
	if event.ParticipantCount == 1 {
		return true, "no human participants remain"
	}
	return false, ""
}
