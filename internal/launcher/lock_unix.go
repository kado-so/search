//go:build !windows

package launcher

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func withPlatformLock(root string, action func() error) error {
	lock, err := os.OpenFile(filepath.Join(root, ".update.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	return action()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
