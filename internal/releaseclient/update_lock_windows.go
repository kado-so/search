//go:build windows

package releaseclient

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"golang.org/x/sys/windows"
)

func acquireUpdateLock(target string) (func(), error) {
	digest := sha256.Sum256([]byte(target))
	name, err := windows.UTF16PtrFromString("Local\\kado-legacy-update-" + hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, ErrInstall
	}
	handle, err := windows.CreateMutex(nil, true, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, ErrInstall
	}
	if handle == 0 || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrInstall
	}
	return func() {
		_ = windows.ReleaseMutex(handle)
		_ = windows.CloseHandle(handle)
	}, nil
}
