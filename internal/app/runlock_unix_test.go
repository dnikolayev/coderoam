//go:build !windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunLockAcquireRejectsStaleInodeAfterUnlinkRecreate regression-tests the
// unlinked-inode hole: a contender opens the lock path, an outside actor
// removes the file (and another daemon recreates it), and the contender then
// flocks the fd it already holds - a lock on a dead inode that excludes
// nobody. lockedFileMatchesPath is what makes the acquisition loop detect
// this and retry on the live file instead of declaring victory.
func TestRunLockAcquireRejectsStaleInodeAfterUnlinkRecreate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.lock")
	early, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer early.Close()
	// The outside actor removes the path out from under the open fd.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The stale fd still flocks successfully - this IS the hole the check
	// closes: without verification this would count as winning the run lock.
	locked, err := tryLockFile(early)
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("flock on the unlinked inode should succeed; nothing else holds it")
	}
	defer func() { _ = unlockFile(early) }()
	// Verification variant 1: the path is gone entirely.
	same, err := lockedFileMatchesPath(early, path)
	if err != nil {
		t.Fatal(err)
	}
	if same {
		t.Fatal("verification must report a mismatch when the path was unlinked")
	}
	// A second daemon now recreates the path and acquires the live inode,
	// even while the stale fd keeps its lock on the dead one.
	releaseLive, err := acquireRunLockAtAs(path, false, "960000001")
	if err != nil {
		t.Fatalf("live daemon should acquire the recreated path: %v", err)
	}
	defer releaseLive()
	// Verification variant 2: the path exists but names a different inode.
	same, err = lockedFileMatchesPath(early, path)
	if err != nil {
		t.Fatal(err)
	}
	if same {
		t.Fatal("verification must report a mismatch when the path was recreated as a new inode")
	}
	// The live holder's own acquisition passed verification: its fd and the
	// path agree, and its lock is genuinely held.
	if _, err := acquireRunLockAtAs(path, false, "960000002"); err == nil {
		t.Fatal("live holder's lock on the recreated path should refuse contenders")
	}
}

// TestRunLockReleaseWarnsWhenEvicted pins the documented residual window that
// flock-on-a-file cannot close (see the protocol comment in runlock.go): an
// rm of run.lock while held lets a second daemon acquire a fresh file at the
// same path. The guaranteed converged behavior is that the evicted holder
// detects this at release, warns loudly, and does not disturb the live
// holder.
func TestRunLockReleaseWarnsWhenEvicted(t *testing.T) {
	// Not parallel: overrides the package-level runLockWarn hook.
	var warnings []string
	originalWarn := runLockWarn
	runLockWarn = func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { runLockWarn = originalWarn })

	// A clean acquire/release cycle must stay silent.
	cleanPath := filepath.Join(t.TempDir(), "run.lock")
	releaseClean, err := acquireRunLockAt(cleanPath, false)
	if err != nil {
		t.Fatal(err)
	}
	releaseClean()
	if len(warnings) != 0 {
		t.Fatalf("clean release must not warn: %q", warnings)
	}

	path := filepath.Join(t.TempDir(), "run.lock")
	releaseA, err := acquireRunLockAtAs(path, false, "970000001")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The residual: with the path gone, B creates and locks a fresh file
	// while A still holds its flock on the unlinked inode. Both "hold the
	// run lock" until A exits - undetectable by B, detectable by A.
	releaseB, err := acquireRunLockAtAs(path, false, "970000002")
	if err != nil {
		t.Fatalf("second daemon acquiring the recreated path is the documented residual: %v", err)
	}
	defer releaseB()

	releaseA()
	if len(warnings) != 1 {
		t.Fatalf("evicted holder's release must warn exactly once, got %d: %q", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], path) {
		t.Fatalf("warning should name the lock path: %q", warnings[0])
	}
	if got := readRunLockPID(path); got != 970000002 {
		t.Fatalf("evicted holder's release disturbed the live holder: pid=%d", got)
	}
	if _, err := acquireRunLockAtAs(path, false, "970000003"); err == nil {
		t.Fatal("live holder's lock should have survived the evicted release")
	}
}
