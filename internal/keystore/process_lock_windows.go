//go:build windows

package keystore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"golang.org/x/sys/windows"
)

func withProcessLock(identifier string, action func() error) error {
	digest := sha256.Sum256([]byte(identifier))
	name, err := windows.UTF16PtrFromString(
		"Local\\kado-credential-" + hex.EncodeToString(digest[:]),
	)
	if err != nil {
		return storageError("acquire creation lock", ErrUnavailable, err)
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return storageError("acquire creation lock", ErrUnavailable, err)
	}
	if handle == 0 {
		return storageError("acquire creation lock", ErrUnavailable, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	wait, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if err != nil || (wait != windows.WAIT_OBJECT_0 && wait != windows.WAIT_ABANDONED) {
		return storageError("acquire creation lock", ErrUnavailable, err)
	}
	defer func() { _ = windows.ReleaseMutex(handle) }()
	return action()
}
