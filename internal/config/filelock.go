package config

import (
	"errors"
	"os"
	"path/filepath"
)

func withConfigMutationLock(path string, mutate func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lockConfigFile(lock); err != nil {
		return err
	}
	defer func() { _ = unlockConfigFile(lock) }()
	return mutate()
}

func atomicWriteConfig(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
			err = removeErr
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = replaceConfigFile(tmpPath, path); err != nil {
		return err
	}
	return syncConfigDirectory(dir)
}
