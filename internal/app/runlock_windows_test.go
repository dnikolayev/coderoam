//go:build windows

package app

import (
	"strings"
	"testing"
)

// TestTakeoverIncumbentRefusedOnWindows pins the honest --takeover error: no
// SIGTERM exists here, so the acquisition loop must fail fast with a clear
// message instead of timing out into the generic "could not acquire" error.
func TestTakeoverIncumbentRefusedOnWindows(t *testing.T) {
	t.Parallel()
	err := takeoverIncumbent(4242)
	if err == nil {
		t.Fatal("takeover must be refused on windows")
	}
	if !strings.Contains(err.Error(), "not supported on Windows") {
		t.Fatalf("refusal should say takeover is unsupported on windows: %v", err)
	}
	if !strings.Contains(err.Error(), "4242") {
		t.Fatalf("refusal should name the incumbent pid: %v", err)
	}
}
