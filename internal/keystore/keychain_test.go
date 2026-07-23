package keystore

import (
	"errors"
	"strings"
	"sync"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

type fakeKeychainBackend struct {
	mu        sync.Mutex
	values    map[string]string
	getError  error
	setError  error
	deleteErr error
}

func newFakeKeychainBackend() *fakeKeychainBackend {
	return &fakeKeychainBackend{values: make(map[string]string)}
}

func (fake *fakeKeychainBackend) Get(service, account string) (string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.getError != nil {
		return "", fake.getError
	}
	value, found := fake.values[service+"\x00"+account]
	if !found {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (fake *fakeKeychainBackend) Set(service, account, value string) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.setError != nil {
		return fake.setError
	}
	fake.values[service+"\x00"+account] = value
	return nil
}

func (fake *fakeKeychainBackend) Delete(service, account string) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.deleteErr != nil {
		return fake.deleteErr
	}
	key := service + "\x00" + account
	if _, found := fake.values[key]; !found {
		return keyring.ErrNotFound
	}
	delete(fake.values, key)
	return nil
}

func TestOSKeychainStorePersistsAcrossAdapterInstances(t *testing.T) {
	t.Parallel()

	backend := newFakeKeychainBackend()
	first := newOSKeychainStore(backend)
	second := newOSKeychainStore(backend)
	keyMaterial := []byte("long-lived-management-key")

	if err := first.Save(keyMaterial); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := second.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if string(loaded) != string(keyMaterial) {
		t.Fatal("loaded key material differs")
	}
	loaded[0] = 'X'
	again, err := first.Load()
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if again[0] == 'X' {
		t.Fatal("Load() returned backend-owned memory")
	}

	if err := second.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := first.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load() after Delete() error = %v, want ErrNotFound", err)
	}
}

func TestOSKeychainStoreRejectsCorruptRecords(t *testing.T) {
	t.Parallel()

	backend := newFakeKeychainBackend()
	backend.values[defaultKeychainService+"\x00"+defaultKeychainAccount] =
		`{"version":99,"data":"private-key-material"}`
	store := newOSKeychainStore(backend)

	_, err := store.Load()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load() error = %v, want ErrCorrupt", err)
	}
	if strings.Contains(err.Error(), "private-key-material") {
		t.Fatalf("Load() error exposed stored content: %q", err)
	}
}

func TestOSKeychainStoreMapsBackendFailuresWithoutLeakingThem(t *testing.T) {
	t.Parallel()

	private := errors.New("backend included secret=do-not-print")
	backend := newFakeKeychainBackend()
	backend.getError = private
	store := newOSKeychainStore(backend)

	_, err := store.Load()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Load() error = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), private.Error()) {
		t.Fatalf("Load() error exposed backend details: %q", err)
	}
}
