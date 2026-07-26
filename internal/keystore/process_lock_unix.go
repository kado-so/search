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
	return withOpenProcessLock(root, processLockName(identifier), action)
}

func withProcessLockInDirectory(
	directory,
	identifier string,
	action func() error,
) error {
	root, err := openPrivateDirectory(directory, false)
	if err != nil {
		return storageError("acquire creation lock", ErrUnavailable, err)
	}
	defer func() { _ = root.Close() }()
	return withOpenProcessLock(root, agentProcessLockName(identifier), action)
}

func validateProcessLockLocation(directory, identifier string) error {
	root, err := openPrivateDirectory(directory, false)
	if err != nil {
		return storageError("validate creation lock", ErrUnavailable, err)
	}
	defer func() { _ = root.Close() }()
	return ensureSafeDestination(root, agentProcessLockName(identifier))
}

func withOpenProcessLock(
	root *os.Root,
	name string,
	action func() error,
) error {
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

func processLockName(identifier string) string {
	digest := sha256.Sum256([]byte(identifier))
	return hex.EncodeToString(digest[:]) + ".lock"
}

func agentProcessLockName(identifier string) string {
	return ".kado-credential-" + processLockName(identifier)
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
