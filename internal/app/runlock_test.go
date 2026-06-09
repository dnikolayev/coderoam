package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAcquireRunLockFreshAndRelease(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.lock")
	release, err := acquireRunLockAt(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if readRunLockPID(path) != os.Getpid() {
		t.Fatalf("lock not owned by us: pid=%d", readRunLockPID(path))
	}
	release()
	// The lock file is never unlinked; a clean release blanks the recorded
	// pid and drops the OS lock so the next daemon acquires immediately.
	if pid := readRunLockPID(path); pid != 0 {
		t.Fatalf("release should blank the recorded pid, got %d", pid)
	}
	again, err := acquireRunLockAt(path, false)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	again()
}

func TestAcquireRunLockRefusesLiveHolder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.lock")
	// Simulate another live daemon: a distinct owner identity holding the OS
	// lock through a separate file handle in this process.
	release, err := acquireRunLockAtAs(path, false, "910000001")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = acquireRunLockAt(path, false)
	if err == nil {
		t.Fatal("expected refusal while a live daemon holds the lock")
	}
	if !strings.Contains(err.Error(), "a coderoam daemon is already running") ||
		!strings.Contains(err.Error(), "--takeover") {
		t.Fatalf("refusal lost the canonical message: %v", err)
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
	// The incumbent daemon: the OS lock is held in-process while the lock
	// file names the TERM-ignoring child, so the takeover signal hits a
	// process that survives it.
	release, err := acquireRunLockAtAs(path, false, strconv.Itoa(holder.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := acquireRunLockAt(path, true); err == nil {
		t.Fatal("expected takeover to fail while the live holder survives")
	}
	if readRunLockPID(path) != holder.Process.Pid {
		t.Fatal("takeover must not clear a lock held by a live process")
	}
}

func TestAcquireRunLockClearsStaleLock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.lock")
	// A crashed daemon leaves content but no OS lock: that is a stale lock.
	if err := os.WriteFile(path, []byte("2147483600\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := acquireRunLockAt(path, false)
	if err != nil {
		t.Fatalf("should claim a stale lock and acquire: %v", err)
	}
	defer release()
	if readRunLockPID(path) != os.Getpid() {
		t.Fatal("stale lock not replaced with our pid")
	}
}

func TestAcquireRunLockContentionSingleWinner(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.lock")
	const contenders = 16
	type result struct {
		id      int
		release func()
		err     error
	}
	results := make([]result, contenders)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := 920000000 + i
			<-start
			release, err := acquireRunLockAtAs(path, false, strconv.Itoa(id))
			results[i] = result{id: id, release: release, err: err}
		}(i)
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, r := range results {
		if r.err != nil {
			continue
		}
		if winner != -1 {
			t.Fatalf("contenders %d and %d both acquired the lock", results[winner].id, r.id)
		}
		winner = i
	}
	if winner == -1 {
		t.Fatal("no contender acquired the lock")
	}
	if got := readRunLockPID(path); got != results[winner].id {
		t.Fatalf("lock file records %d, want winner %d", got, results[winner].id)
	}
	// Losers must not have weakened the winner's hold.
	if _, err := acquireRunLockAtAs(path, false, "930000000"); err == nil {
		t.Fatal("lock should still be held by the winner")
	}
	results[winner].release()
	release, err := acquireRunLockAt(path, false)
	if err != nil {
		t.Fatalf("acquire after winner released: %v", err)
	}
	release()
}

func TestAcquireRunLockStaleRecoveryRaceSingleWinner(t *testing.T) {
	t.Parallel()
	for round := 0; round < 10; round++ {
		path := filepath.Join(t.TempDir(), "run.lock")
		// A dead owner's leftover lock that both contenders judge stale.
		if err := os.WriteFile(path, []byte("2147483600\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		type result struct {
			id      int
			release func()
			err     error
		}
		results := make([]result, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id := 940000001 + i
				<-start
				release, err := acquireRunLockAtAs(path, false, strconv.Itoa(id))
				results[i] = result{id: id, release: release, err: err}
			}(i)
		}
		close(start)
		wg.Wait()

		winner := -1
		for i, r := range results {
			if r.err != nil {
				continue
			}
			if winner != -1 {
				t.Fatalf("round %d: both contenders claimed the stale lock", round)
			}
			winner = i
		}
		if winner == -1 {
			t.Fatalf("round %d: nobody claimed the stale lock", round)
		}
		if got := readRunLockPID(path); got != results[winner].id {
			t.Fatalf("round %d: lock file records %d, want winner %d", round, got, results[winner].id)
		}
		// The loser must not have destroyed the winner's lock: probing the
		// OS lock directly must still find it held.
		probe, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		locked, err := tryLockFile(probe)
		if err != nil {
			t.Fatal(err)
		}
		if locked {
			t.Fatalf("round %d: winner's lock was destroyed by the loser", round)
		}
		_ = probe.Close()
		results[winner].release()
		release, err := acquireRunLockAt(path, false)
		if err != nil {
			t.Fatalf("round %d: acquire after winner released: %v", round, err)
		}
		release()
	}
}

func TestAcquireRunLockReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.lock")
	release, err := acquireRunLockAt(path, false)
	if err != nil {
		t.Fatal(err)
	}
	release()
	again, err := acquireRunLockAtAs(path, false, "950000001")
	if err != nil {
		t.Fatal(err)
	}
	defer again()
	// A second release of the first lock must not disturb the new holder.
	release()
	if got := readRunLockPID(path); got != 950000001 {
		t.Fatalf("double release disturbed the new holder: pid=%d", got)
	}
	if _, err := acquireRunLockAt(path, false); err == nil {
		t.Fatal("lock should still be held after the stale double release")
	}
}

func TestProcessAlive(t *testing.T) {
	t.Parallel()
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
