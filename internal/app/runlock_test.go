package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAcquireRunLockFreshAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.lock")
	release, err := acquireRunLockAt(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if readRunLockPID(path) != os.Getpid() {
		t.Fatalf("lock not owned by us: pid=%d", readRunLockPID(path))
	}
	release()
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("release should remove the lock file")
	}
}

func TestAcquireRunLockRefusesLiveHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.lock")
	// Simulate another live daemon holding the lock using the parent pid
	// (alive and different from ours). takeover=false so nothing is signaled.
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getppid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireRunLockAt(path, false); err == nil {
		t.Fatal("expected refusal while a live daemon holds the lock")
	}
}

func TestAcquireRunLockTakeoverDoesNotClearLiveHolderThatSurvives(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM ignore fixture is POSIX-only")
	}
	originalTimeout := runLockTakeoverTimeout
	runLockTakeoverTimeout = 150 * time.Millisecond
	t.Cleanup(func() { runLockTakeoverTimeout = originalTimeout })

	holder := exec.Command("sh", "-c", "trap '' TERM; sleep 30")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
	})

	path := filepath.Join(t.TempDir(), "run.lock")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", holder.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireRunLockAt(path, true); err == nil {
		t.Fatal("expected takeover to fail while the live holder survives")
	}
	if readRunLockPID(path) != holder.Process.Pid {
		t.Fatal("takeover must not clear a lock held by a live process")
	}
}

func TestAcquireRunLockClearsStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.lock")
	// A dead pid (very high, almost certainly not running) is a stale lock.
	if err := os.WriteFile(path, []byte("2147483600\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := acquireRunLockAt(path, false)
	if err != nil {
		t.Fatalf("should clear a stale lock and acquire: %v", err)
	}
	defer release()
	if readRunLockPID(path) != os.Getpid() {
		t.Fatal("stale lock not replaced with our pid")
	}
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("current process should be alive")
	}
	if processAlive(2147483600) {
		t.Fatal("an unused high pid should not be alive")
	}
	if processAlive(0) || processAlive(-1) {
		t.Fatal("non-positive pids are never alive")
	}
}
