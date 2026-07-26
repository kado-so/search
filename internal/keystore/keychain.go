package keystore

import (
	"crypto/subtle"
	"errors"
	"strings"

	keyring "github.com/zalando/go-keyring"
)

const (
	defaultKeychainService = "kado.so"
	defaultKeychainAccount = "agent-management-key.v1"
)

type keychainBackend interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

type systemKeychainBackend struct{}

func (systemKeychainBackend) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (systemKeychainBackend) Set(service, account, value string) error {
	return keyring.Set(service, account, value)
}

func (systemKeychainBackend) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

// OSKeychainStore persists the management key through macOS Keychain, Windows
// Credential Manager, or a Secret Service-compatible keyring on Linux/BSD.
type OSKeychainStore struct {
	backend keychainBackend
	service string
	account string
}

// NewOSKeychainStore returns the preferred long-lived key store for one
// canonical local agent identity.
func NewOSKeychainStore(agent string) (*OSKeychainStore, error) {
	if !validAgentNamespace(agent) {
		return nil, storageError("configure keychain", ErrInvalid, nil)
	}
	return newOSKeychainStore(
		systemKeychainBackend{},
		defaultKeychainAccount+":"+agent,
	), nil
}

func newOSKeychainStore(backend keychainBackend, account ...string) *OSKeychainStore {
	selected := defaultKeychainAccount
	if len(account) == 1 {
		selected = account[0]
	}
	return &OSKeychainStore{
		backend: backend,
		service: defaultKeychainService,
		account: selected,
	}
}

func validAgentNamespace(agent string) bool {
	if agent == "" || len(agent) > 64 {
		return false
	}
	for _, character := range agent {
		if !(character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '-') {
			return false
		}
	}
	return !strings.HasPrefix(agent, "-") && !strings.HasSuffix(agent, "-")
}

func (store *OSKeychainStore) Load() ([]byte, error) {
	encoded, err := store.backend.Get(store.service, store.account)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, storageError("load", ErrNotFound, err)
		}
		return nil, storageError("load", ErrUnavailable, err)
	}
	keyMaterial, err := decodeRecord(encoded)
	if err != nil {
		return nil, storageError("load", ErrCorrupt, err)
	}
	return keyMaterial, nil
}

func (store *OSKeychainStore) Create(keyMaterial []byte) ([]byte, bool, error) {
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

func (store *OSKeychainStore) Save(keyMaterial []byte) error {
	return withProcessLock(store.lockIdentifier(), func() error {
		return store.saveUnlocked(keyMaterial)
	})
}

func (store *OSKeychainStore) saveUnlocked(keyMaterial []byte) error {
	encoded, err := encodeRecord(keyMaterial)
	if err != nil {
		return err
	}
	if err := store.backend.Set(store.service, store.account, encoded); err != nil {
		return storageError("save", ErrUnavailable, err)
	}
	return nil
}

func (store *OSKeychainStore) Delete() error {
	return withProcessLock(store.lockIdentifier(), store.deleteUnlocked)
}

func (store *OSKeychainStore) deleteUnlocked() error {
	if err := store.backend.Delete(store.service, store.account); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return storageError("delete", ErrNotFound, err)
		}
		return storageError("delete", ErrUnavailable, err)
	}
	return nil
}

func (store *OSKeychainStore) DeleteIfMatches(expected []byte) (bool, error) {
	if len(expected) == 0 || len(expected) > maxKeyMaterialBytes {
		return false, storageError("conditionally delete", ErrInvalid, nil)
	}
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
		if err := store.deleteUnlocked(); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	return deleted, err
}

func (store *OSKeychainStore) lockIdentifier() string {
	return "keychain:" + store.service + "\x00" + store.account
}
