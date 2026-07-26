package agentkey

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kado-so/search/internal/keystore"
)

func TestManagementSignerPersistsAcrossFileStoreInstances(t *testing.T) {
	t.Parallel()

	root := filepath.Join(resolvedFileStoreTempDir(t), "secrets")
	firstStore, err := keystore.NewAgentFileStore(root, "default")
	if err != nil {
		t.Fatalf("NewAgentFileStore(first) error = %v", err)
	}
	created := deterministicManagementSigner(t)
	if err := SaveManagementSigner(firstStore, created); err != nil {
		t.Fatalf("SaveManagementSigner() error = %v", err)
	}

	secondStore, err := keystore.NewAgentFileStore(root, "default")
	if err != nil {
		t.Fatalf("NewAgentFileStore(second) error = %v", err)
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

	privateRoot := filepath.Join(
		resolvedFileStoreTempDir(t),
		"private-name-must-not-leak",
	)
	store, err := keystore.NewAgentFileStore(privateRoot, "default")
	if err != nil {
		t.Fatalf("NewAgentFileStore() error = %v", err)
	}
	_, err = LoadManagementSigner(store)
	if !errors.Is(err, ErrPersistence) || !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("LoadManagementSigner() error = %v", err)
	}
	if bytes.Contains([]byte(err.Error()), []byte(privateRoot)) {
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
