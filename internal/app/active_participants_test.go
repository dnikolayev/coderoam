package app

import (
	"strings"
	"testing"

	"github.com/dnikolayev/coderoam/internal/config"
)

func TestActiveDefaultParticipants(t *testing.T) {
	t.Parallel()
	var cfg config.Config
	cfg.Security.AdminSenderIDs = []string{"owner@lid", "15550001111@s.whatsapp.net", "15550001111@s.whatsapp.net"}
	cfg.Security.AllowedSenderIDs = []string{"owner@lid", "friend@lid", "1203630@g.us", "+15550002222", ""}
	got := activeDefaultParticipants(cfg)
	if strings.Join(got, ",") != "15550001111@s.whatsapp.net,+15550002222" {
		t.Fatalf("activeDefaultParticipants = %v, want phone-addressable participants", got)
	}
	if len(activeDefaultParticipants(config.Config{})) != 0 {
		t.Fatal("empty config should yield no default participants")
	}
}
