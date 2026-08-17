//go:build windows

package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"golang.org/x/sys/windows"
)

func withPlatformLock(root string, action func() error) error {
	digest := sha256.Sum256([]byte(root))
	name, err := windows.UTF16PtrFromString("Local\\kado-update-" + hex.EncodeToString(digest[:]))
	if err != nil {
		return err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	if handle == 0 {
		return errors.New("launcher update mutex is unavailable")
	}
	defer windows.CloseHandle(handle)
	wait, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if err != nil || wait != windows.WAIT_OBJECT_0 && wait != windows.WAIT_ABANDONED {
		return errors.New("launcher update mutex could not be acquired")
	}
	defer windows.ReleaseMutex(handle)
	return action()
}

func syncDirectory(string) error { return nil }
