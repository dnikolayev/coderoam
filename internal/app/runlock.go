package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dnikolayev/coderoam/internal/config"
)

var runLockTakeoverTimeout = 8 * time.Second

func runLockPath(profile string) string {
	return filepath.Join(config.ProfileDir(profile), "run.lock")
}

// acquireRunLock enforces a single orchestrator daemon per profile: the one
// process that owns the messenger connection. It returns a release function. If
// another live daemon already holds the lock it refuses, unless takeover is set
// (which signals the incumbent to stop, then claims the lock).
func acquireRunLock(profile string, takeover bool) (func(), error) {
	return acquireRunLockAt(runLockPath(profile), takeover)
}

// Lock protocol
// -------------
// The run lock is an OS advisory lock (flock on unix, LockFileEx on windows;
// see tryLockFile/unlockFile) held on a lock file that is NEVER unlinked or
// renamed. The file content is the holder's pid, written while already
// holding the lock; it is diagnostic only (refusal messages, takeover
// signaling) and plays no part in mutual exclusion.
//
// Why this shape:
//   - Acquisition is arbitrated entirely by the kernel on a single stable
//     inode, so there is no check-then-act window: two contenders can never
//     both win, regardless of interleaving. The previous pid-file protocol
//     (judge stale -> unlink -> O_EXCL create) let two racing daemons both
//     judge a dead owner's lock stale, both unlink, and both "win" - and a
//     laggard could even unlink the fresh winner's lock.
//   - Crash recovery is implicit: the kernel drops the lock when the holder
//     dies, so a "stale lock" is just an unlocked file with leftover content.
//     Claiming it needs no unlink; the next winner overwrites the content
//     while already holding the lock.
//   - Never unlinking the file closes the ABA race where one contender locks
//     an inode that another has just unlinked from the path, after which a
//     third creates a fresh file there and locks it too - two holders on two
//     different inodes behind one path.
//
// Remaining (accepted) windows, none of which break mutual exclusion:
//   - A contender may read the previous owner's pid before the new winner
//     overwrites it, so a refusal message or takeover signal can name a pid
//     that just exited. Signaling a dead pid is a no-op, and the takeover
//     loop re-reads and retries until the lock is free or attempts run out.
//   - If the OS recycles a pid between the holder recording it and a
//     takeover signaling it, an unrelated process receives SIGTERM. Inherent
//     to recording pids; the window is tiny because the pid is written
//     immediately after acquisition and blanked on clean release.
func acquireRunLockAt(path string, takeover bool) (func(), error) {
	return acquireRunLockAtAs(path, takeover, strconv.Itoa(os.Getpid()))
}

// acquireRunLockAtAs is acquireRunLockAt with an explicit owner identity (the
// pid recorded in the lock file). Production always records the real pid;
// tests inject distinct pid-like identities to simulate separate owners
// contending from within one process.
func acquireRunLockAtAs(path string, takeover bool, ownerID string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	myPID, _ := strconv.Atoi(strings.TrimSpace(ownerID))
	for attempt := 0; attempt < 100; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		locked, err := tryLockFile(f)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		if locked {
			if err := writeRunLockOwner(f, ownerID); err != nil {
				_ = unlockFile(f)
				_ = f.Close()
				return nil, err
			}
			var once sync.Once
			return func() {
				once.Do(func() {
					// Blank the recorded pid while still holding the lock so
					// no reader sees stale content after a clean exit, then
					// let the close drop the kernel lock. The file itself
					// stays in place forever (see protocol above).
					_ = f.Truncate(0)
					_ = unlockFile(f)
					_ = f.Close()
				})
			}, nil
		}
		_ = f.Close()
		holder := readRunLockPID(path)
		switch {
		case holder > 0 && holder == myPID:
			// Held by this very process through another handle; never signal
			// ourselves, even for takeover.
			return nil, fmt.Errorf("a coderoam daemon is already running (pid %d); stop it or rerun with --takeover", holder)
		case holder > 0 && !takeover:
			return nil, fmt.Errorf("a coderoam daemon is already running (pid %d); stop it or rerun with --takeover", holder)
		case holder > 0:
			_ = signalProcess(holder, syscall.SIGTERM)
			if !waitForProcessExit(holder, runLockTakeoverTimeout) {
				return nil, fmt.Errorf("a coderoam daemon is still running after takeover signal (pid %d)", holder)
			}
		case !takeover && attempt >= 20:
			// Locked, but the holder has not recorded its pid yet (we raced
			// its acquisition); after a grace period refuse without one.
			return nil, fmt.Errorf("a coderoam daemon is already running; stop it or rerun with --takeover")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil, fmt.Errorf("could not acquire run lock at %s", path)
}

// writeRunLockOwner records the owner pid in the lock file. Only ever called
// while holding the OS lock, so concurrent writers are impossible.
func writeRunLockOwner(f *os.File, ownerID string) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt([]byte(ownerID+"\n"), 0); err != nil {
		return err
	}
	_ = f.Sync()
	return nil
}

func readRunLockPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// processAlive reports whether pid is a live process. Correct on Unix; on
// Windows (no signal 0) it degrades to permissive, which only weakens the
// takeover wait, never mutual exclusion (the kernel lock arbitrates that).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func signalProcess(pid int, sig os.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

func waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !processAlive(pid)
}
