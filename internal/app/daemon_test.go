package app

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/db"
	"github.com/dnikolayev/coderoam/internal/types"
)

// TestHandleRelayGroupLifecycleEventDoesNotMutateCallerConfig pins the
// replace-not-mutate contract: archiving a relay group must build a fresh
// Groups slice rather than writing through the backing array shared with the
// caller's snapshot. Before the fix this test failed because the shared entry
// was flipped to Archived in place.
func TestHandleRelayGroupLifecycleEventDoesNotMutateCallerConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "daemon-test.sqlite3"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer store.Close()
	if err := store.EnsureProfile(ctx, "bot"); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	original := config.Config{}
	original.App.Profile = "bot"
	original.Groups = []config.GroupConfig{{
		ID:              "123@g.us",
		Alias:           "relay",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "relay-session",
		Enabled:         true,
		RelayManaged:    true,
	}}
	// shared aliases the same backing array as original.Groups, standing in
	// for every other goroutine still reading the pre-event snapshot.
	shared := original.Groups

	event := types.GroupEvent{
		ChatID:             "123@g.us",
		SenderID:           "owner@s.whatsapp.net",
		LeftParticipantIDs: []string{"owner@s.whatsapp.net"},
		ParticipantCount:   2,
		Timestamp:          time.Now(),
	}
	updated, archived, err := handleRelayGroupLifecycleEvent(ctx, original, filepath.Join(dir, "config.toml"), store, nil, event)
	if err != nil {
		t.Fatalf("handleRelayGroupLifecycleEvent: %v", err)
	}
	if !archived {
		t.Fatal("expected the participant-left event to archive the relay group")
	}
	if !updated.Groups[0].Archived || updated.Groups[0].Enabled {
		t.Fatalf("updated config should carry the archived group, got %+v", updated.Groups[0])
	}
	if shared[0].Archived || !shared[0].Enabled || shared[0].ArchivedAt != "" {
		t.Fatalf("handleRelayGroupLifecycleEvent mutated the shared Groups backing array: %+v", shared[0])
	}
}

// TestRunConfigHolderConcurrentLoadStore hammers the holder from a writer and
// several readers under -race and checks each loaded snapshot is internally
// consistent (profile and groups always belong to the same stored config).
// The holder is new with the fix, so this pins post-fix semantics; the
// pre-fix code shared a bare local variable with no equivalent to exercise.
func TestRunConfigHolderConcurrentLoadStore(t *testing.T) {
	t.Parallel()
	configA := config.Config{}
	configA.App.Profile = "profile-a"
	configA.Groups = []config.GroupConfig{{ID: "chat-a@g.us", Enabled: true}}

	configB := config.Config{}
	configB.App.Profile = "profile-b"
	configB.Groups = []config.GroupConfig{
		{ID: "chat-b@g.us", Enabled: true},
		{ID: "chat-c@g.us", Enabled: true, Archived: true},
	}

	holder := newRunConfigHolder(configA)

	done := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			if i%2 == 0 {
				holder.Store(configB)
			} else {
				holder.Store(configA)
			}
		}
	}()

	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 5000; i++ {
				cfg := holder.Load()
				switch cfg.App.Profile {
				case "profile-a":
					if len(cfg.Groups) != 1 || cfg.Groups[0].ID != "chat-a@g.us" {
						t.Errorf("torn read: profile-a paired with groups %+v", cfg.Groups)
						return
					}
				case "profile-b":
					if len(cfg.Groups) != 2 || cfg.Groups[1].ID != "chat-c@g.us" {
						t.Errorf("torn read: profile-b paired with groups %+v", cfg.Groups)
						return
					}
				default:
					t.Errorf("torn read: unexpected profile %q", cfg.App.Profile)
					return
				}
			}
		}()
	}
	readers.Wait()
	close(done)
	writer.Wait()
}

func TestRunConfigHolderDeepClonesReferenceFields(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	cfg.App.Profile = "bot"
	cfg.Security.AdminSenderIDs = []string{"admin@lid"}
	cfg.Security.AllowedSenderIDs = []string{"allowed@lid"}
	cfg.Groups = []config.GroupConfig{{
		ID:              "chat@g.us",
		Alias:           "codex-session",
		Mode:            config.GroupModeActiveSession,
		ActiveSessionID: "codex-session",
		Enabled:         true,
	}}
	cfg.Runner = map[string]config.RunnerConfig{
		"codex-code": {
			Mode:    "process-once-json",
			Command: "/bin/codex-runner",
			Args:    []string{"--prompt"},
			Env:     map[string]string{"SESSION": "codex-session"},
		},
	}

	holder := newRunConfigHolder(cfg)

	cfg.Groups[0].ID = "mutated-chat@g.us"
	cfg.Security.AdminSenderIDs[0] = "mutated-admin@lid"
	cfg.Security.AllowedSenderIDs[0] = "mutated-allowed@lid"
	runnerCfg := cfg.Runner["codex-code"]
	runnerCfg.Args[0] = "--mutated"
	runnerCfg.Env["SESSION"] = "mutated-session"
	cfg.Runner["codex-code"] = runnerCfg

	loaded := holder.Load()
	if loaded.Groups[0].ID != "chat@g.us" {
		t.Fatalf("Store did not clone groups: %+v", loaded.Groups)
	}
	if loaded.Security.AdminSenderIDs[0] != "admin@lid" || loaded.Security.AllowedSenderIDs[0] != "allowed@lid" {
		t.Fatalf("Store did not clone sender allowlists: admin=%+v allowed=%+v", loaded.Security.AdminSenderIDs, loaded.Security.AllowedSenderIDs)
	}
	loadedRunner := loaded.Runner["codex-code"]
	if loadedRunner.Args[0] != "--prompt" || loadedRunner.Env["SESSION"] != "codex-session" {
		t.Fatalf("Store did not clone runner config: %+v", loadedRunner)
	}

	loaded.Groups[0].ID = "loaded-mutated-chat@g.us"
	loaded.Security.AdminSenderIDs[0] = "loaded-mutated-admin@lid"
	loaded.Security.AllowedSenderIDs[0] = "loaded-mutated-allowed@lid"
	loadedRunner.Args[0] = "--loaded-mutated"
	loadedRunner.Env["SESSION"] = "loaded-mutated-session"
	loaded.Runner["codex-code"] = loadedRunner

	reloaded := holder.Load()
	reloadedRunner := reloaded.Runner["codex-code"]
	if reloaded.Groups[0].ID != "chat@g.us" ||
		reloaded.Security.AdminSenderIDs[0] != "admin@lid" ||
		reloaded.Security.AllowedSenderIDs[0] != "allowed@lid" ||
		reloadedRunner.Args[0] != "--prompt" ||
		reloadedRunner.Env["SESSION"] != "codex-session" {
		t.Fatalf("Load exposed mutable snapshot state: cfg=%+v runner=%+v", reloaded, reloadedRunner)
	}
}

func TestRunConfigHolderZeroValueLoad(t *testing.T) {
	t.Parallel()
	var holder runConfigHolder
	got := holder.Load()
	if got.App.Profile != "" || len(got.Groups) != 0 || len(got.Runner) != 0 || len(got.Security.AdminSenderIDs) != 0 || len(got.Security.AllowedSenderIDs) != 0 {
		t.Fatalf("zero-value holder load = %+v", got)
	}
}
