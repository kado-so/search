//go:build !windows

package maintenance

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func withStateLock(configDir string, action func() error) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(configDir, ".maintenance.lock"), os.O_RDWR|os.O_CREATE, 0o600)
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
