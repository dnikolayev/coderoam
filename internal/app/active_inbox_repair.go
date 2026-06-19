package app

import (
	"context"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
)

func repairActiveInboxForConfig(ctx context.Context, store *db.Store, cfg config.Config) (int, error) {
	return store.RepairOrphanedActiveInbox(ctx, cfg.App.Profile, activeSessionBindings(cfg), activeInboxClaimStaleAfter)
}

func repairActiveInboxForSession(ctx context.Context, store *db.Store, cfg config.Config, sessionID string) (int, error) {
	return store.RepairOrphanedActiveInboxForSessions(ctx, cfg.App.Profile, activeSessionBindings(cfg), []string{sessionID}, activeInboxClaimStaleAfter)
}

func repairableActiveInboxCounts(ctx context.Context, store *db.Store, cfg config.Config) (map[string]int, int, error) {
	counts, err := store.RepairableActiveInboxCounts(ctx, cfg.App.Profile, activeSessionBindings(cfg), activeInboxClaimStaleAfter)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	for _, count := range counts {
		total += count
	}
	return counts, total, nil
}

func activeSessionBindings(cfg config.Config) []db.ActiveSessionBinding {
	bindings := []db.ActiveSessionBinding{}
	for _, group := range cfg.Groups {
		if !group.Enabled || group.Archived || group.Mode != config.GroupModeActiveSession {
			continue
		}
		bindings = append(bindings, db.ActiveSessionBinding{
			ChatID:    group.ID,
			ChatAlias: group.Alias,
			SessionID: config.ActiveSessionID(group),
		})
	}
	return bindings
}
