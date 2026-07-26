package keystore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(TempDir()) error = %v", err)
	}
	return resolved
}

func TestFileStoreIsExplicitAndPermissionRestricted(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		if _, err := NewFileStore(`C:\kado\management-key`); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("NewFileStore() error = %v, want ErrUnsupported", err)
		}
		return
	}

	storePath := filepath.Join(resolvedTempDir(t), "private", "management-key.json")
	store, err := NewFileStore(storePath)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	keyMaterial := []byte("persistent-management-key")
	if err := store.Save(keyMaterial); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	directoryInfo, err := os.Stat(filepath.Dir(storePath))
	if err != nil {
		t.Fatalf("Stat(directory) error = %v", err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %o, want 700", directoryInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(storePath)
	if err != nil {
		t.Fatalf("Stat(file) error = %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600", fileInfo.Mode().Perm())
	}

	reopened, err := NewFileStore(storePath)
	if err != nil {
		t.Fatalf("NewFileStore(reopen) error = %v", err)
	}
	loaded, err := reopened.Load()
	if err != nil {
		t.Fatalf("reopened Load() error = %v", err)
	}
	if string(loaded) != string(keyMaterial) {
		t.Fatal("loaded key material differs")
	}
	if err := reopened.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load() after Delete() error = %v, want ErrNotFound", err)
	}
}

func TestIsolatedFileStoreRequiresPreprovisionedSafeLocation(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		if _, err := NewIsolatedFileStore(`C:\kado\management-key`); !errors.Is(
			err,
			ErrUnsupported,
		) {
			t.Fatalf("NewIsolatedFileStore() error = %v, want ErrUnsupported", err)
		}
		return
	}

	root := resolvedTempDir(t)
	privateDirectory := filepath.Join(root, "private")
	if err := os.Mkdir(privateDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(private) error = %v", err)
	}
	storePath := filepath.Join(privateDirectory, "management-key.json")
	store, err := NewIsolatedFileStore(storePath)
	if err != nil {
		t.Fatalf("NewIsolatedFileStore() error = %v", err)
	}
	if err := store.Save([]byte("isolated-management-key")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := NewIsolatedFileStore(storePath); err != nil {
		t.Fatalf("NewIsolatedFileStore(existing) error = %v", err)
	}

	for _, test := range []struct {
		name string
		path string
		kind error
	}{
		{
			name: "empty",
			path: "",
			kind: ErrInvalid,
		},
		{
			name: "relative",
			path: filepath.Join("relative", "management-key.json"),
			kind: ErrInvalid,
		},
		{
			name: "unclean",
			path: privateDirectory + string(filepath.Separator) +
				".." + string(filepath.Separator) +
				filepath.Base(privateDirectory) + string(filepath.Separator) +
				"other.json",
			kind: ErrInvalid,
		},
		{
			name: "control",
			path: filepath.Join(privateDirectory, "other\n.json"),
			kind: ErrInvalid,
		},
		{
			name: "missing parent",
			path: filepath.Join(root, "missing", "management-key.json"),
			kind: ErrNotFound,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewIsolatedFileStore(test.path); !errors.Is(err, test.kind) {
				t.Fatalf(
					"NewIsolatedFileStore() error = %v, want %v",
					err,
					test.kind,
				)
			}
		})
	}
}

func TestIsolatedFileStoreRejectsSymlinksAndUnsafePermissionsAtSelection(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}

	root := resolvedTempDir(t)
	privateDirectory := filepath.Join(root, "private")
	if err := os.Mkdir(privateDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(private) error = %v", err)
	}
	storePath := filepath.Join(privateDirectory, "management-key.json")
	if err := os.WriteFile(storePath, []byte("opaque"), 0o600); err != nil {
		t.Fatalf("WriteFile(store) error = %v", err)
	}
	if err := os.Chmod(storePath, 0o644); err != nil {
		t.Fatalf("Chmod(store) error = %v", err)
	}
	if _, err := NewIsolatedFileStore(storePath); !errors.Is(err, ErrPermissions) {
		t.Fatalf("unsafe file error = %v, want ErrPermissions", err)
	}
	if err := os.Remove(storePath); err != nil {
		t.Fatalf("Remove(store) error = %v", err)
	}
	if err := os.Chmod(privateDirectory, 0o755); err != nil {
		t.Fatalf("Chmod(private) error = %v", err)
	}
	if _, err := NewIsolatedFileStore(storePath); !errors.Is(err, ErrPermissions) {
		t.Fatalf("unsafe directory error = %v, want ErrPermissions", err)
	}
	if err := os.Chmod(privateDirectory, 0o700); err != nil {
		t.Fatalf("Chmod(private restore) error = %v", err)
	}

	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(real) error = %v", err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDirectory, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewIsolatedFileStore(
		filepath.Join(alias, "management-key.json"),
	); !errors.Is(err, ErrPermissions) {
		t.Fatalf("symlink parent error = %v, want ErrPermissions", err)
	}
}

func TestIsolatedFileStoreNeverRecreatesRemovedParent(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}

	root := resolvedTempDir(t)
	privateDirectory := filepath.Join(root, "private")
	if err := os.Mkdir(privateDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(private) error = %v", err)
	}
	storePath := filepath.Join(privateDirectory, "management-key.json")
	store, err := NewIsolatedFileStore(storePath)
	if err != nil {
		t.Fatalf("NewIsolatedFileStore() error = %v", err)
	}
	if err := os.Remove(privateDirectory); err != nil {
		t.Fatalf("Remove(private) error = %v", err)
	}

	if err := store.Save([]byte("must-not-be-written")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Save() error = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(privateDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Save() recreated isolated parent: %v", err)
	}
	if _, _, err := store.Create(
		[]byte("must-not-be-created"),
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Create() error = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(privateDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Create() recreated isolated parent: %v", err)
	}
}

func TestFileStoreConditionalDeleteRetainsReplacement(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}
	storePath := filepath.Join(resolvedTempDir(t), "private", "management-key.json")
	store, err := NewFileStore(storePath)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	oldKey := []byte("management-key-A")
	newKey := []byte("management-key-B")
	if err := store.Save(oldKey); err != nil {
		t.Fatalf("Save(old key) error = %v", err)
	}
	if err := store.Save(newKey); err != nil {
		t.Fatalf("Save(new key) error = %v", err)
	}
	deleted, err := store.DeleteIfMatches(oldKey)
	if err != nil {
		t.Fatalf("DeleteIfMatches(old key) error = %v", err)
	}
	if deleted {
		t.Fatal("DeleteIfMatches(old key) deleted replacement")
	}
	deleted, err = store.DeleteIfMatches(newKey)
	if err != nil || !deleted {
		t.Fatalf("DeleteIfMatches(new key) deleted=%t error=%v", deleted, err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load() after matched delete error = %v, want ErrNotFound", err)
	}
}

func TestFileStoreRejectsInsecureDirectoryAndFileModes(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}

	root := resolvedTempDir(t)
	insecureDirectory := filepath.Join(root, "insecure")
	if err := os.Mkdir(insecureDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.Chmod(insecureDirectory, 0o755); err != nil {
		t.Fatalf("Chmod(directory) error = %v", err)
	}
	store, err := NewFileStore(filepath.Join(insecureDirectory, "management-key.json"))
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if err := store.Save([]byte("secret")); !errors.Is(err, ErrPermissions) {
		t.Fatalf("Save() error = %v, want ErrPermissions", err)
	}

	privateDirectory := filepath.Join(root, "private")
	store, err = NewFileStore(filepath.Join(privateDirectory, "management-key.json"))
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if err := store.Save([]byte("secret")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := os.Chmod(store.path, 0o644); err != nil {
		t.Fatalf("Chmod(file) error = %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrPermissions) {
		t.Fatalf("Load() error = %v, want ErrPermissions", err)
	}
}

func TestFileStoreRejectsSymlinksAndCorruptionWithoutLeakingContent(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}

	storePath := filepath.Join(resolvedTempDir(t), "private", "management-key.json")
	store, err := NewFileStore(storePath)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if err := store.Save([]byte("secret")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	privateContent := `{"version":99,"data":"private-key-material-do-not-print"}`
	if err := os.WriteFile(storePath, []byte(privateContent), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err = store.Load()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load() error = %v, want ErrCorrupt", err)
	}
	if strings.Contains(err.Error(), "private-key-material") {
		t.Fatalf("Load() error exposed file contents: %q", err)
	}

	decoy := filepath.Join(filepath.Dir(storePath), "decoy")
	if err := os.WriteFile(decoy, []byte("decoy"), 0o600); err != nil {
		t.Fatalf("WriteFile(decoy) error = %v", err)
	}
	if err := os.Remove(storePath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.Symlink(decoy, storePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrPermissions) {
		t.Fatalf("Load(symlink) error = %v, want ErrPermissions", err)
	}
	if err := store.Save([]byte("replacement")); !errors.Is(err, ErrPermissions) {
		t.Fatalf("Save(symlink) error = %v, want ErrPermissions", err)
	}
}

func TestFileStoreRejectsUnknownFormatInsteadOfMigrating(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}

	storePath := filepath.Join(resolvedTempDir(t), "private", "management-key.json")
	store, err := NewFileStore(storePath)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if err := store.Save([]byte("secret")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := os.WriteFile(
		storePath,
		[]byte(`{"version":0,"data":"bGVnYWN5"}`),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load(legacy) error = %v, want ErrCorrupt", err)
	}
}

func TestFileStoreRejectsSymlinkInAnyAncestor(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}

	root := resolvedTempDir(t)
	realRoot := filepath.Join(root, "real")
	privateDirectory := filepath.Join(realRoot, "private")
	if err := os.MkdirAll(privateDirectory, 0o700); err != nil {
		t.Fatalf("MkdirAll(private) error = %v", err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	store, err := NewFileStore(filepath.Join(alias, "private", "management-key.json"))
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if err := store.Save([]byte("secret")); !errors.Is(err, ErrPermissions) {
		t.Fatalf("Save(ancestor symlink) error = %v, want ErrPermissions", err)
	}
}

func TestAnchoredDirectorySurvivesAncestorReplacement(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file fallback is intentionally unsupported on Windows")
	}

	root := resolvedTempDir(t)
	original := filepath.Join(root, "original")
	originalPrivate := filepath.Join(original, "private")
	decoy := filepath.Join(root, "decoy")
	decoyPrivate := filepath.Join(decoy, "private")
	for _, directory := range []string{originalPrivate, decoyPrivate} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", directory, err)
		}
	}

	anchored, err := openPrivateDirectory(originalPrivate, false)
	if err != nil {
		t.Fatalf("openPrivateDirectory() error = %v", err)
	}
	defer func() { _ = anchored.Close() }()

	moved := filepath.Join(root, "moved")
	if err := os.Rename(original, moved); err != nil {
		t.Fatalf("Rename(original) error = %v", err)
	}
	if err := os.Symlink(decoy, original); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	encoded, err := encodeRecord([]byte("anchored-secret"))
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	if err := saveFileRecord(anchored, "management-key.json", encoded); err != nil {
		t.Fatalf("saveFileRecord() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "private", "management-key.json")); err != nil {
		t.Fatalf("anchored destination Stat() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(decoyPrivate, "management-key.json")); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("decoy destination unexpectedly changed: %v", err)
	}
}
