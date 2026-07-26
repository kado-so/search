package agentkey

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kado-so/search/internal/keystore"
)

func TestManagementSignerPersistsAcrossFileStoreInstances(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}

	storePath := filepath.Join(resolvedFileStoreTempDir(t), "private", "management-key.json")
	firstStore, err := keystore.NewFileStore(storePath)
	if err != nil {
		t.Fatalf("NewFileStore(first) error = %v", err)
	}
	created := deterministicManagementSigner(t)
	if err := SaveManagementSigner(firstStore, created); err != nil {
		t.Fatalf("SaveManagementSigner() error = %v", err)
	}

	secondStore, err := keystore.NewFileStore(storePath)
	if err != nil {
		t.Fatalf("NewFileStore(second) error = %v", err)
	}
	loaded, err := LoadManagementSigner(secondStore)
	if err != nil {
		t.Fatalf("LoadManagementSigner() error = %v", err)
	}
	if !bytes.Equal(
		created.Public().(ed25519.PublicKey),
		loaded.Public().(ed25519.PublicKey),
	) {
		t.Fatal("management identity changed across store instances")
	}
}

func TestFileStorePersistenceErrorDoesNotExposePath(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}

	privatePath := filepath.Join(
		resolvedFileStoreTempDir(t),
		"private-name-must-not-leak",
		"key.json",
	)
	store, err := keystore.NewFileStore(privatePath)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	_, err = LoadManagementSigner(store)
	if !errors.Is(err, ErrPersistence) || !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("LoadManagementSigner() error = %v", err)
	}
	if bytes.Contains([]byte(err.Error()), []byte(privatePath)) {
		t.Fatalf("persistence error exposed private path: %q", err)
	}
}

func resolvedFileStoreTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(TempDir()) error = %v", err)
	}
	return resolved
}
