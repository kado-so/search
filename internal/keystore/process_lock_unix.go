//go:build !windows

package keystore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func withProcessLock(identifier string, action func() error) error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return storageError("acquire creation lock", ErrUnavailable, err)
	}
	lockDirectory := filepath.Join(cache, "kado", "credential-locks")
	root, err := openPrivateDirectory(lockDirectory, true)
	if err != nil {
		return storageError("acquire creation lock", ErrUnavailable, err)
	}
	defer func() { _ = root.Close() }()

	digest := sha256.Sum256([]byte(identifier))
	name := hex.EncodeToString(digest[:]) + ".lock"
	lock, err := openOrCreatePrivateFile(root, name)
	if err != nil {
		return storageError("acquire creation lock", ErrUnavailable, err)
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return storageError("acquire creation lock", ErrUnavailable, err)
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	return action()
}

func openOrCreatePrivateFile(root *os.Root, name string) (*os.File, error) {
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if chmodErr := file.Chmod(0o600); chmodErr != nil {
			_ = file.Close()
			_ = root.Remove(name)
			return nil, chmodErr
		}
		return file, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return openPrivateFile(root, name)
}
