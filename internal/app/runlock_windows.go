//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Windows byte-range locks are mandatory, so the exclusive lock is taken on a
// single byte far beyond any real file content. That keeps mutual exclusion
// (all contenders lock the same fixed range) while leaving the few pid bytes
// at offset 0 readable and writable. The OS releases the lock when the
// holding process exits, however it exits.
const runLockRangeOffsetHigh = 0x7FFFFFFF

func runLockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{Offset: 0, OffsetHigh: runLockRangeOffsetHigh}
}

// tryLockFile attempts a non-blocking exclusive LockFileEx on f. It returns
// (true, nil) when the lock was acquired and (false, nil) when another handle
// holds it. Locks belong to the handle, so two handles within one process
// still conflict - which is what lets the tests stage real contention.
func tryLockFile(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, runLockOverlapped())
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, runLockOverlapped())
}

// lockedFileMatchesPath reports whether path still names the locked file. On
// Windows this cannot become false while f is open: the lock file is opened
// with os.OpenFile, whose syscall.Open passes sharemode FILE_SHARE_READ |
// FILE_SHARE_WRITE *without* FILE_SHARE_DELETE (verified against go1.26
// src/syscall/syscall_windows.go), so DeleteFile and MoveFileEx on the path
// fail with a sharing violation for as long as the handle exists. The unix
// unlink-and-recreate hole is therefore structurally impossible here, and the
// fstat/stat compare is intentionally not ported.
func lockedFileMatchesPath(_ *os.File, _ string) (bool, error) {
	return true, nil
}

// takeoverIncumbent refuses takeover on Windows: there is no SIGTERM here
// (os.Process.Signal supports only Kill), so coderoam cannot ask the incumbent
// daemon to shut down cleanly. Failing fast with an honest message beats the
// old behavior of looping until the generic "could not acquire run lock".
func takeoverIncumbent(holder int) error {
	return fmt.Errorf("takeover is not supported on Windows; stop the running coderoam process (pid %d) manually and rerun", holder)
}
