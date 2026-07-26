package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/keystore"
)

func TestDefaultCredentialStoreRemainsOSKeychain(t *testing.T) {
	t.Parallel()

	store, err := selectDefaultCredentialStore(func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatalf("selectDefaultCredentialStore() error = %v", err)
	}
	if _, ok := store.(*keystore.OSKeychainStore); !ok {
		t.Fatalf("default store type = %T, want *keystore.OSKeychainStore", store)
	}
}

func TestAcceptanceCredentialStorePersistsAcrossSelectionsWithoutKeychain(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("isolated file credentials are intentionally unsupported on Windows")
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(TempDir()) error = %v", err)
	}
	privateDirectory := filepath.Join(root, "credentials")
	if err := os.Mkdir(privateDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(credentials) error = %v", err)
	}
	storePath := filepath.Join(privateDirectory, "management-key.json")
	environment := func(name string) (string, bool) {
		if name != acceptanceCredentialFileEnvironment {
			t.Fatalf("environment lookup name = %q", name)
		}
		return storePath, true
	}

	authSelection, err := selectDefaultCredentialStore(environment)
	if err != nil {
		t.Fatalf("auth selection error = %v", err)
	}
	if _, ok := authSelection.(*keystore.FileStore); !ok {
		t.Fatalf("acceptance store type = %T, want *keystore.FileStore", authSelection)
	}
	keyMaterial := []byte("process-persistent-management-key")
	winning, created, err := authSelection.Create(keyMaterial)
	if err != nil || !created || !bytes.Equal(winning, keyMaterial) {
		t.Fatalf("Create() created=%t error=%v", created, err)
	}

	searchSelection, err := selectDefaultCredentialStore(environment)
	if err != nil {
		t.Fatalf("search selection error = %v", err)
	}
	loaded, err := searchSelection.Load()
	if err != nil {
		t.Fatalf("Load() through second selection error = %v", err)
	}
	if !bytes.Equal(loaded, keyMaterial) {
		t.Fatal("auth and search selections did not resolve the same credential")
	}
}

func TestAcceptanceCredentialStoreRejectsUnsafeLocationWithoutPathLeak(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("isolated file credentials are intentionally unsupported on Windows")
	}

	privatePath := filepath.Join(
		"relative",
		"PRIVATE-CREDENTIAL-PATH",
		"management-key.json",
	)
	t.Setenv(acceptanceCredentialFileEnvironment, privatePath)
	t.Setenv("KADO_BASE_URL", "https://kado.so")
	t.Setenv("KADO_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))

	for _, args := range [][]string{
		{"auth", "status"},
		{"search", "selector validation must precede network access"},
	} {
		var stdout, stderr bytes.Buffer
		exitCode := Run(args, &stdout, &stderr, buildinfo.Info{})
		if exitCode != diagnostic.ExitFailure || stdout.Len() != 0 {
			t.Fatalf(
				"Run(%q) exit=%d stdout=%q stderr=%q",
				args,
				exitCode,
				stdout.String(),
				stderr.String(),
			)
		}
		if strings.Contains(stderr.String(), privatePath) ||
			strings.Contains(stderr.String(), "PRIVATE-CREDENTIAL-PATH") {
			t.Fatalf("Run(%q) leaked configured path: %q", args, stderr.String())
		}
	}
}

func TestAcceptanceCredentialStoreRejectsExplicitEmptyValue(t *testing.T) {
	t.Parallel()

	_, err := selectDefaultCredentialStore(func(string) (string, bool) {
		return "", true
	})
	want := keystore.ErrInvalid
	if runtime.GOOS == "windows" {
		want = keystore.ErrUnsupported
	}
	if !errors.Is(err, want) {
		t.Fatalf("selectDefaultCredentialStore(empty) error = %v, want %v", err, want)
	}
}
