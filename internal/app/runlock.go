package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func acquireRunLockAt(path string, takeover bool) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	myPID := os.Getpid()
	for attempt := 0; attempt < 100; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", myPID)
			_ = f.Close()
			return func() {
				if readRunLockPID(path) == myPID {
					_ = os.Remove(path)
				}
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		holder := readRunLockPID(path)
		if holder > 0 && holder != myPID && processAlive(holder) {
			if !takeover {
				return nil, fmt.Errorf("a coderoam daemon is already running (pid %d); stop it or rerun with --takeover", holder)
			}
			_ = signalProcess(holder, syscall.SIGTERM)
			if !waitForProcessExit(holder, runLockTakeoverTimeout) {
				return nil, fmt.Errorf("a coderoam daemon is still running after takeover signal (pid %d)", holder)
			}
			continue
		}
		// Stale lock (holder dead/unreadable) or our own leftover; clear and retry.
		_ = os.Remove(path)
	}
	return nil, fmt.Errorf("could not acquire run lock at %s", path)
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
// Windows (no signal 0) it degrades to permissive, which only weakens the lock.
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
