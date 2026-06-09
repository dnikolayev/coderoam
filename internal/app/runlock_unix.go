//go:build !windows

package app

import (
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
