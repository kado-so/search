package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/keystore"
)

const acceptanceCredentialFileEnvironment = "KADO_ACCEPTANCE_CREDENTIAL_FILE"

func TestActualCLISelectsIsolatedCredentialWithoutKeychain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("isolated file credentials are intentionally unsupported on Windows")
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(TempDir()) error = %v", err)
	}
	credentialDirectory := filepath.Join(root, "credentials")
	if err := os.Mkdir(credentialDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(credentials) error = %v", err)
	}
	processHome := filepath.Join(root, "process-home")
	processCache := filepath.Join(root, "process-cache")
	for _, directory := range []string{processHome, processCache} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("Mkdir(process isolation) error = %v", err)
		}
	}
	credentialPath := filepath.Join(credentialDirectory, "management-key.json")
	store, err := keystore.NewIsolatedFileStore(credentialPath)
	if err != nil {
		t.Fatalf("NewIsolatedFileStore() error = %v", err)
	}

	moduleRoot := cliModuleRoot(t)
	binaryPath := filepath.Join(root, "kado")
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/kado")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/kado error = %v output=%q", err, output)
	}
	environment := append(
		environmentWithout(
			"KADO_BASE_URL",
			"KADO_CONFIG_DIR",
			acceptanceCredentialFileEnvironment,
			"HOME",
			"XDG_CACHE_HOME",
		),
		"KADO_BASE_URL=https://127.0.0.1:1",
		"KADO_CONFIG_DIR="+filepath.Join(root, "config"),
		acceptanceCredentialFileEnvironment+"="+credentialPath,
		"HOME="+processHome,
		"XDG_CACHE_HOME="+processCache,
	)

	stdout, stderr, exitCode := runActualCLI(
		t,
		binaryPath,
		environment,
		"auth",
		"status",
	)
	if exitCode != 0 || stderr != "" || stdout != "status: not-configured\n" {
		t.Fatalf(
			"empty status exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout,
			stderr,
		)
	}

	privateMarker := []byte("PRIVATE-FILE-STORE-MARKER")
	if err := store.Save(privateMarker); err != nil {
		t.Fatalf("Save(marker) error = %v", err)
	}
	stdout, stderr, exitCode = runActualCLI(
		t,
		binaryPath,
		environment,
		"auth",
		"status",
	)
	if exitCode == 0 ||
		stdout != "" ||
		!strings.Contains(stderr, "[auth_status_failed]") ||
		strings.Contains(stderr, string(privateMarker)) ||
		strings.Contains(stderr, credentialPath) {
		t.Fatalf(
			"stored marker status exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout,
			stderr,
		)
	}
	loaded, err := store.Load()
	if err != nil || !bytes.Equal(loaded, privateMarker) {
		t.Fatalf("CLI changed isolated record: loaded=%q error=%v", loaded, err)
	}

	if err := store.Delete(); err != nil {
		t.Fatalf("Delete(marker) error = %v", err)
	}
	stdout, stderr, exitCode = runActualCLI(
		t,
		binaryPath,
		environment,
		"auth",
		"status",
	)
	if exitCode != 0 || stderr != "" || stdout != "status: not-configured\n" {
		t.Fatalf(
			"cleaned status exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout,
			stderr,
		)
	}
	if _, err := os.Stat(credentialDirectory); err != nil {
		t.Fatalf("credential directory was not caller-owned: %v", err)
	}
	entries, err := os.ReadDir(credentialDirectory)
	if err != nil {
		t.Fatalf("ReadDir(credentials) error = %v", err)
	}
	if len(entries) != 1 ||
		!strings.HasPrefix(entries[0].Name(), ".kado-credential-") ||
		!strings.HasSuffix(entries[0].Name(), ".lock") {
		t.Fatalf("credential directory residue = %v, want one isolated lock", entries)
	}
	for _, directory := range []string{processHome, processCache} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("ReadDir(process isolation) error = %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("process isolation directory received residue: %v", entries)
		}
	}
	if err := os.RemoveAll(credentialDirectory); err != nil {
		t.Fatalf("acceptance credential cleanup error = %v", err)
	}
	if _, err := os.Stat(credentialDirectory); !os.IsNotExist(err) {
		t.Fatalf("acceptance credential cleanup left residue: %v", err)
	}
}

func cliModuleRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	root, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	if err != nil {
		t.Fatalf("EvalSymlinks(module root) error = %v", err)
	}
	return root
}

func environmentWithout(names ...string) []string {
	excluded := make(map[string]struct{}, len(names))
	for _, name := range names {
		excluded[name] = struct{}{}
	}
	filtered := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		name, _, found := strings.Cut(value, "=")
		if !found {
			continue
		}
		if _, remove := excluded[name]; !remove {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func runActualCLI(
	t *testing.T,
	binaryPath string,
	environment []string,
	arguments ...string,
) (string, string, int) {
	t.Helper()
	command := exec.Command(binaryPath, arguments...)
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run %q error = %v", arguments, err)
	}
	return stdout.String(), stderr.String(), exitError.ExitCode()
}
