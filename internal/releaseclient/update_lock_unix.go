//go:build !windows

package releaseclient

import (
	"os"

	"golang.org/x/sys/unix"
)

func acquireUpdateLock(target string) (func(), error) {
	lock, err := os.OpenFile(target+".update.lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, ErrInstall
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, ErrInstall
	}
	return func() {
		_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		_ = lock.Close()
	}, nil
}
