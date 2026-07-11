//go:build windows

package config

import (
	"os"

	"golang.org/x/sys/windows"
)

const configLockRangeOffsetHigh = 0x7FFFFFFE

func configLockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{Offset: 0, OffsetHigh: configLockRangeOffsetHigh}
}

func lockConfigFile(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, configLockOverlapped())
}

func unlockConfigFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, configLockOverlapped())
}

func replaceConfigFile(from string, to string) error {
	fromPath, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPath, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPath, toPath, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func syncConfigDirectory(string) error {
	return nil
}
