//go:build windows

package app

import (
	"errors"
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
