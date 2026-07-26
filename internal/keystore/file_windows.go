//go:build windows

package keystore

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
)

type FileStore struct {
	path string
}

func newFileStore(path string) (*FileStore, error) {
	if path == "" ||
		path != strings.TrimSpace(path) ||
		strings.ContainsFunc(path, unicode.IsControl) ||
		!filepath.IsAbs(path) ||
		path != filepath.Clean(path) {
		return nil, storageError("configure file fallback", ErrInvalid, nil)
	}
	store := &FileStore{path: path}
	if err := store.ValidateLocation(); err != nil {
		return nil, err
	}
	return store, nil
}

func NewAgentFileStore(root, agent string) (*FileStore, error) {
	if !validAgentNamespace(agent) ||
		root == "" ||
		!filepath.IsAbs(root) ||
		root != filepath.Clean(root) {
		return nil, storageError("configure file fallback", ErrInvalid, nil)
	}
	agentDirectory := filepath.Join(root, agent)
	if err := os.MkdirAll(agentDirectory, 0o700); err != nil {
		return nil, storageError("prepare file fallback", ErrUnavailable, err)
	}
	for _, path := range []string{root, agentDirectory} {
		if err := rejectWindowsReparsePoint(path, true); err != nil {
			return nil, err
		}
		if err := applyPrivateWindowsACL(path); err != nil {
			return nil, storageError("secure file fallback", ErrPermissions, err)
		}
	}
	return newFileStore(filepath.Join(agentDirectory, "management-key.json"))
}

func (store *FileStore) ValidateLocation() error {
	if store == nil {
		return storageError("validate file fallback", ErrInvalid, nil)
	}
	parent := filepath.Dir(store.path)
	if err := rejectWindowsReparsePoint(parent, true); err != nil {
		return err
	}
	if err := applyPrivateWindowsACL(parent); err != nil {
		return storageError("secure file fallback", ErrPermissions, err)
	}
	if _, err := os.Lstat(store.path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return storageError("access file fallback", ErrUnavailable, err)
	}
	if err := rejectWindowsReparsePoint(store.path, false); err != nil {
		return err
	}
	return applyPrivateWindowsACL(store.path)
}

func (store *FileStore) Load() ([]byte, error) {
	if err := store.ValidateLocation(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, storageError("load file fallback", ErrNotFound, err)
		}
		return nil, err
	}
	encoded, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, storageError("load file fallback", ErrNotFound, err)
	}
	if err != nil || len(encoded) > maxEncodedBytes*2 {
		return nil, storageError("load file fallback", ErrUnavailable, err)
	}
	protected, err := base64.RawStdEncoding.Strict().DecodeString(string(encoded))
	if err != nil {
		return nil, storageError("load file fallback", ErrCorrupt, err)
	}
	plain, err := unprotectWindows(protected)
	clear(protected)
	if err != nil {
		return nil, storageError("load file fallback", ErrCorrupt, err)
	}
	defer clear(plain)
	keyMaterial, err := decodeRecord(string(plain))
	if err != nil {
		return nil, storageError("load file fallback", ErrCorrupt, err)
	}
	return keyMaterial, nil
}

func (store *FileStore) Create(keyMaterial []byte) ([]byte, bool, error) {
	var winning []byte
	var created bool
	err := withProcessLock(store.lockIdentifier(), func() error {
		existing, err := store.Load()
		if err == nil {
			winning = existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := store.saveUnlocked(keyMaterial); err != nil {
			return err
		}
		winning = append([]byte(nil), keyMaterial...)
		created = true
		return nil
	})
	return winning, created, err
}

func (store *FileStore) Save(keyMaterial []byte) error {
	return withProcessLock(store.lockIdentifier(), func() error {
		return store.saveUnlocked(keyMaterial)
	})
}

func (store *FileStore) saveUnlocked(keyMaterial []byte) error {
	encoded, err := encodeRecord(keyMaterial)
	if err != nil {
		return err
	}
	plain := []byte(encoded)
	protected, err := protectWindows(plain)
	clear(plain)
	if err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	defer clear(protected)
	value := []byte(base64.RawStdEncoding.EncodeToString(protected))
	defer clear(value)
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".kado-management-key-*")
	if err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	name := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(name)
	}()
	if err := applyPrivateWindowsACL(name); err != nil {
		return storageError("save file fallback", ErrPermissions, err)
	}
	if _, err := temporary.Write(value); err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	if err := temporary.Sync(); err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	if err := temporary.Close(); err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	source, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	destination, err := windows.UTF16PtrFromString(store.path)
	if err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	if err := windows.MoveFileEx(
		source,
		destination,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	); err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	if err := applyPrivateWindowsACL(store.path); err != nil {
		return storageError("save file fallback", ErrPermissions, err)
	}
	return nil
}

func (store *FileStore) Delete() error {
	return withProcessLock(store.lockIdentifier(), func() error {
		if err := os.Remove(store.path); errors.Is(err, os.ErrNotExist) {
			return storageError("delete file fallback", ErrNotFound, err)
		} else if err != nil {
			return storageError("delete file fallback", ErrUnavailable, err)
		}
		return nil
	})
}

func (store *FileStore) DeleteIfMatches(expected []byte) (bool, error) {
	deleted := false
	err := withProcessLock(store.lockIdentifier(), func() error {
		current, err := store.Load()
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		defer clear(current)
		if subtle.ConstantTimeCompare(current, expected) != 1 {
			return nil
		}
		if err := os.Remove(store.path); err != nil {
			return storageError("delete file fallback", ErrUnavailable, err)
		}
		deleted = true
		return nil
	})
	return deleted, err
}

func (store *FileStore) lockIdentifier() string {
	return "file:" + store.path
}

func rejectWindowsReparsePoint(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return storageError("access file fallback", ErrUnavailable, err)
	}
	if info.Mode()&os.ModeSymlink != 0 ||
		directory && !info.IsDir() ||
		!directory && !info.Mode().IsRegular() {
		return storageError("access file fallback", ErrPermissions, nil)
	}
	attributes, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(path))
	if err != nil {
		return storageError("access file fallback", ErrUnavailable, err)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return storageError("access file fallback", ErrPermissions, nil)
	}
	return nil
}

func applyPrivateWindowsACL(path string) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
			},
		},
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(system),
			},
		},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}

func protectWindows(value []byte) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(value))}
	if len(value) > 0 {
		input.Data = &value[0]
	}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, 0, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}

func unprotectWindows(value []byte) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(value))}
	if len(value) > 0 {
		input.Data = &value[0]
	}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, 0, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
