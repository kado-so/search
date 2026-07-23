package keystore

import (
	"errors"

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

// NewOSKeychainStore returns the preferred long-lived key store.
func NewOSKeychainStore() *OSKeychainStore {
	return newOSKeychainStore(systemKeychainBackend{})
}

func newOSKeychainStore(backend keychainBackend) *OSKeychainStore {
	return &OSKeychainStore{
		backend: backend,
		service: defaultKeychainService,
		account: defaultKeychainAccount,
	}
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

func (store *OSKeychainStore) Save(keyMaterial []byte) error {
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
	if err := store.backend.Delete(store.service, store.account); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return storageError("delete", ErrNotFound, err)
		}
		return storageError("delete", ErrUnavailable, err)
	}
	return nil
}
