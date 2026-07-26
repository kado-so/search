//go:build !windows

package keystore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentProcessLockUsesPrivateNamespace(t *testing.T) {
	t.Parallel()

	const identifier = "file:/private/agent/management-key.json"
	digest := sha256.Sum256([]byte(identifier))
	global := hex.EncodeToString(digest[:]) + ".lock"
	if got := processLockName(identifier); got != global {
		t.Fatalf("global lock name = %q, want %q", got, global)
	}
	if got, want := agentProcessLockName(identifier),
		".kado-credential-"+global; got != want {
		t.Fatalf("agent lock name = %q, want %q", got, want)
	}
}

func TestAgentFileStoreRejectsUnsafeLocalProcessLockAtSelection(t *testing.T) {
	t.Parallel()

	root := resolvedTempDir(t)
	privateDirectory := filepath.Join(root, "private")
	if err := os.Mkdir(privateDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(private) error = %v", err)
	}
	storePath := filepath.Join(privateDirectory, "management-key.json")
	unselected, err := newFileStore(storePath)
	if err != nil {
		t.Fatalf("newFileStore() error = %v", err)
	}
	lockPath := filepath.Join(
		privateDirectory,
		agentProcessLockName(unselected.lockIdentifier()),
	)
	if err := os.WriteFile(lockPath, []byte("unsafe"), 0o644); err != nil {
		t.Fatalf("WriteFile(lock) error = %v", err)
	}
	if _, err := newFileStore(storePath); !errors.Is(err, ErrPermissions) {
		t.Fatalf("unsafe lock error = %v, want ErrPermissions", err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("Remove(lock) error = %v", err)
	}
	decoy := filepath.Join(privateDirectory, "decoy")
	if err := os.WriteFile(decoy, []byte("decoy"), 0o600); err != nil {
		t.Fatalf("WriteFile(decoy) error = %v", err)
	}
	if err := os.Symlink(decoy, lockPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := newFileStore(storePath); !errors.Is(err, ErrPermissions) {
		t.Fatalf("symlink lock error = %v, want ErrPermissions", err)
	}
}
