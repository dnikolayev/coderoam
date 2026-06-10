//go:build !windows

package app

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// tryLockFile attempts a non-blocking exclusive flock(2) on f. It returns
// (true, nil) when the lock was acquired and (false, nil) when another open
// file description holds it. flock locks belong to the open file description,
// so two handles within one process still conflict - which is what lets the
// tests stage real contention - and the kernel releases the lock when the
// holding process dies, however it dies.
func tryLockFile(f *os.File) (bool, error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch err {
		case nil:
			return true, nil
		case syscall.EINTR:
			continue
		case syscall.EWOULDBLOCK:
			return false, nil
		default:
			return false, err
		}
	}
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// lockedFileMatchesPath reports whether path still names the very inode the
// locked handle f holds (fstat-vs-stat dev+inode compare via os.SameFile). A
// flock survives an unlink of its path, but then excludes nobody who arrives
// through that path; acquisition retries on false, and release warns. A
// missing path counts as a mismatch, not an error: the retry recreates it.
func lockedFileMatchesPath(f *os.File, path string) (bool, error) {
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	pi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return os.SameFile(fi, pi), nil
}

// takeoverIncumbent asks the live daemon recorded in the lock file to stop
// (SIGTERM) and waits for it to exit, so the caller's acquisition loop can
// claim the freed lock on its next attempt.
func takeoverIncumbent(holder int) error {
	_ = signalProcess(holder, syscall.SIGTERM)
	if !waitForProcessExit(holder, runLockTakeoverTimeout) {
		return fmt.Errorf("a coderoam daemon is still running after takeover signal (pid %d)", holder)
	}
	return nil
}
