//go:build !windows

package config

import (
	"os"
	"syscall"
)

func lockConfigFile(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if err == syscall.EINTR {
			continue
		}
		return err
	}
}

func unlockConfigFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func replaceConfigFile(from string, to string) error {
	return os.Rename(from, to)
}

func syncConfigDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
