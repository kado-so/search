//go:build windows

package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func withStateLock(configDir string, action func() error) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(configDir))
	name, err := windows.UTF16PtrFromString("Local\\kado-maintenance-" + hex.EncodeToString(digest[:]))
	if err != nil {
		return err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	if handle == 0 {
		return errors.New("maintenance mutex is unavailable")
	}
	defer windows.CloseHandle(handle)
	wait, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if err != nil || wait != windows.WAIT_OBJECT_0 && wait != windows.WAIT_ABANDONED {
		return errors.New("maintenance mutex could not be acquired")
	}
	defer windows.ReleaseMutex(handle)
	return action()
}
