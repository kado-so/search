//go:build !windows

package keystore

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const temporaryFileAttempts = 100

// FileStore is an explicit fallback for systems where an OS keychain cannot be
// used. It is never selected automatically.
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
	if err := validateProcessLockLocation(
		filepath.Dir(store.path),
		store.lockIdentifier(),
	); err != nil {
		return nil, err
	}
	return store, nil
}

// NewAgentFileStore creates private per-agent directories beneath root.
func NewAgentFileStore(root, agent string) (*FileStore, error) {
	if !validAgentNamespace(agent) ||
		root == "" ||
		!filepath.IsAbs(root) ||
		root != filepath.Clean(root) {
		return nil, storageError("configure file fallback", ErrInvalid, nil)
	}
	privateRoot, err := openPrivateDirectory(root, true)
	if err != nil {
		return nil, err
	}
	_ = privateRoot.Close()
	agentDirectory := filepath.Join(root, agent)
	privateAgent, err := openPrivateDirectory(agentDirectory, true)
	if err != nil {
		return nil, err
	}
	_ = privateAgent.Close()
	return newFileStore(
		filepath.Join(agentDirectory, "management-key.json"),
	)
}

// ValidateLocation verifies that the configured parent is an existing exact
// 0700 directory with no symlink components and that an existing destination
// is a regular exact 0600 file. A missing destination is valid.
func (store *FileStore) ValidateLocation() error {
	if store == nil {
		return storageError("validate file fallback", ErrInvalid, nil)
	}
	parent, err := openPrivateDirectory(filepath.Dir(store.path), false)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	return ensureSafeDestination(parent, filepath.Base(store.path))
}

func (store *FileStore) Load() ([]byte, error) {
	parent, err := openPrivateDirectory(filepath.Dir(store.path), false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()

	file, err := openPrivateFile(parent, filepath.Base(store.path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedBytes+1))
	if err != nil {
		return nil, storageError("load file fallback", ErrUnavailable, err)
	}
	if len(encoded) > maxEncodedBytes {
		return nil, storageError("load file fallback", ErrCorrupt, nil)
	}
	keyMaterial, err := decodeRecord(string(encoded))
	if err != nil {
		return nil, storageError("load file fallback", ErrCorrupt, err)
	}
	return keyMaterial, nil
}

func (store *FileStore) Create(keyMaterial []byte) ([]byte, bool, error) {
	var winning []byte
	var created bool
	err := store.withProcessLock(func() error {
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
	return store.withProcessLock(func() error {
		return store.saveUnlocked(keyMaterial)
	})
}

func (store *FileStore) saveUnlocked(keyMaterial []byte) error {
	encoded, err := encodeRecord(keyMaterial)
	if err != nil {
		return err
	}
	parent := filepath.Dir(store.path)
	parentRoot, err := openPrivateDirectory(parent, false)
	if err != nil {
		return err
	}
	defer func() { _ = parentRoot.Close() }()
	return saveFileRecord(parentRoot, filepath.Base(store.path), encoded)
}

func (store *FileStore) Delete() error {
	return store.withProcessLock(store.deleteUnlocked)
}

func (store *FileStore) deleteUnlocked() error {
	parent, err := openPrivateDirectory(filepath.Dir(store.path), false)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()

	name := filepath.Base(store.path)
	if err := ensurePrivateFile(parent, name); err != nil {
		return err
	}
	if err := parent.Remove(name); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return storageError("delete file fallback", ErrNotFound, err)
		}
		return storageError("delete file fallback", ErrUnavailable, err)
	}
	if err := syncDirectory(parent); err != nil {
		return storageError("delete file fallback", ErrUnavailable, err)
	}
	return nil
}

func (store *FileStore) DeleteIfMatches(expected []byte) (bool, error) {
	if len(expected) == 0 || len(expected) > maxKeyMaterialBytes {
		return false, storageError("conditionally delete file fallback", ErrInvalid, nil)
	}
	deleted := false
	err := store.withProcessLock(func() error {
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
		if err := store.deleteUnlocked(); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	return deleted, err
}

func (store *FileStore) lockIdentifier() string {
	return "file:" + store.path
}

func (store *FileStore) withProcessLock(action func() error) error {
	return withProcessLockInDirectory(
		filepath.Dir(store.path),
		store.lockIdentifier(),
		action,
	)
}

// openPrivateDirectory walks from the filesystem root one component at a time,
// rejects every symlink, and retains an anchored handle to each opened
// directory. Comparing Lstat with the opened handle closes component
// replacement races without relying on path-based operations after validation.
func openPrivateDirectory(path string, create bool) (*os.Root, error) {
	volumeRoot := filepath.VolumeName(path) + string(filepath.Separator)
	relative, err := filepath.Rel(volumeRoot, path)
	if err != nil || relative == "." || relative == "" {
		return nil, storageError("access file fallback", ErrPermissions, err)
	}

	current, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, storageError("access file fallback", ErrUnavailable, err)
	}
	components := strings.Split(relative, string(filepath.Separator))
	for index, component := range components {
		info, componentErr := lstatOrCreateDirectory(current, component, create)
		if componentErr != nil {
			_ = current.Close()
			return nil, componentErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			_ = current.Close()
			return nil, storageError("access file fallback", ErrPermissions, nil)
		}

		next, openErr := current.OpenRoot(component)
		if openErr != nil {
			_ = current.Close()
			return nil, storageError("access file fallback", ErrUnavailable, openErr)
		}
		openedInfo, statErr := next.Lstat(".")
		if statErr != nil || !os.SameFile(info, openedInfo) {
			_ = next.Close()
			_ = current.Close()
			return nil, storageError("access file fallback", ErrPermissions, statErr)
		}
		_ = current.Close()
		current = next

		if index == len(components)-1 && openedInfo.Mode().Perm() != 0o700 {
			_ = current.Close()
			return nil, storageError("access file fallback", ErrPermissions, nil)
		}
	}
	return current, nil
}

func lstatOrCreateDirectory(root *os.Root, name string, create bool) (os.FileInfo, error) {
	for {
		info, err := root.Lstat(name)
		if err == nil {
			return info, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, storageError("access file fallback", ErrUnavailable, err)
		}
		if !create {
			return nil, storageError("access file fallback", ErrNotFound, err)
		}
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, storageError("prepare file fallback", ErrUnavailable, err)
		}
	}
}

func ensureSafeDestination(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return storageError("access file fallback", ErrUnavailable, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return storageError("access file fallback", ErrPermissions, nil)
	}
	if info.Mode().Perm() != 0o600 {
		return storageError("access file fallback", ErrPermissions, nil)
	}
	return nil
}

func ensurePrivateFile(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return storageError("access file fallback", ErrNotFound, err)
	}
	if err != nil {
		return storageError("access file fallback", ErrUnavailable, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return storageError("access file fallback", ErrPermissions, nil)
	}
	return nil
}

func openPrivateFile(root *os.Root, name string) (*os.File, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, storageError("access file fallback", ErrNotFound, err)
	}
	if err != nil {
		return nil, storageError("access file fallback", ErrUnavailable, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, storageError("access file fallback", ErrPermissions, nil)
	}

	file, err := root.Open(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, storageError("access file fallback", ErrNotFound, err)
		}
		return nil, storageError("access file fallback", ErrUnavailable, err)
	}
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, storageError("access file fallback", ErrPermissions, err)
	}
	return file, nil
}

func saveFileRecord(root *os.Root, name, encoded string) error {
	if err := ensureSafeDestination(root, name); err != nil {
		return err
	}
	temporary, temporaryName, err := createTemporaryFile(root)
	if err != nil {
		return err
	}
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = root.Remove(temporaryName)
		}
	}()

	if _, err := io.WriteString(temporary, encoded); err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	if err := temporary.Sync(); err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	if err := temporary.Close(); err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	directory, err := root.Open(".")
	if err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	if err := renameWithinDirectory(directory, temporaryName, name); err != nil {
		_ = directory.Close()
		return storageError("save file fallback", ErrUnavailable, err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return storageError("save file fallback", ErrUnavailable, err)
	}
	if err := directory.Close(); err != nil {
		return storageError("save file fallback", ErrUnavailable, err)
	}
	keepTemporary = false
	return ensurePrivateFile(root, name)
}

func createTemporaryFile(root *os.Root) (*os.File, string, error) {
	var random [16]byte
	for range temporaryFileAttempts {
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", storageError("save file fallback", ErrUnavailable, err)
		}
		name := ".kado-management-key-" + hex.EncodeToString(random[:])
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", storageError("save file fallback", ErrUnavailable, err)
		}
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			_ = root.Remove(name)
			return nil, "", storageError("save file fallback", ErrPermissions, err)
		}
		return file, name, nil
	}
	return nil, "", storageError("save file fallback", ErrUnavailable, nil)
}

func syncDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync key directory: %w", err)
	}
	return nil
}
