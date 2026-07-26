package cli

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/agentkey"
)

const (
	credentialStoreProcessHelperEnvironment = "KADO_CREDENTIAL_STORE_PROCESS_HELPER"
	credentialStoreProcessActionEnvironment = "KADO_CREDENTIAL_STORE_PROCESS_ACTION"
)

func TestAcceptanceCredentialStorePersistsAcrossProcessesAndCleansOnlyItsRecord(
	t *testing.T,
) {
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

	first := runInterruptedCredentialStoreHelper(t, credentialPath)
	second := runCredentialStoreHelper(t, credentialPath, "load-or-create")
	if first == "" || first != second {
		t.Fatal("process after interruption did not reuse the isolated management identity")
	}
	if _, err := os.Stat(credentialPath); err != nil {
		t.Fatalf("credential did not persist across processes: %v", err)
	}

	deleted := runCredentialStoreHelper(t, credentialPath, "delete")
	if deleted != "deleted" {
		t.Fatalf("delete helper result = %q", deleted)
	}
	if _, err := os.Stat(credentialPath); !os.IsNotExist(err) {
		t.Fatalf("credential remains after explicit delete: %v", err)
	}
	info, err := os.Stat(credentialDirectory)
	if err != nil {
		t.Fatalf("caller-owned credential directory was removed: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("caller-owned directory mode = %o, want 700", info.Mode().Perm())
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
	lockInfo, err := entries[0].Info()
	if err != nil {
		t.Fatalf("Info(isolated lock) error = %v", err)
	}
	if !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("isolated lock mode = %v, want regular 0600", lockInfo.Mode())
	}
	for _, directory := range []string{processHome, processCache} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("ReadDir(process isolation) error = %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("isolated process directory received residue: %v", entries)
		}
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("acceptance runner cleanup error = %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("acceptance runner left residue: %v", err)
	}
}

func TestCredentialStoreProcessHelper(t *testing.T) {
	if os.Getenv(credentialStoreProcessHelperEnvironment) != "1" {
		return
	}
	store, err := newDefaultCredentialStore()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "credential store selection failed")
		os.Exit(2)
	}
	switch os.Getenv(credentialStoreProcessActionEnvironment) {
	case "load-or-create", "load-or-create-and-wait":
		signer, _, err := agentkey.LoadOrCreateManagementSigner(store)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "credential creation failed")
			os.Exit(3)
		}
		public := signer.Public().(ed25519.PublicKey)
		_, _ = fmt.Fprintln(
			os.Stdout,
			base64.RawURLEncoding.EncodeToString(public),
		)
		if os.Getenv(credentialStoreProcessActionEnvironment) ==
			"load-or-create-and-wait" {
			select {}
		}
		os.Exit(0)
	case "delete":
		if err := store.Delete(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "credential deletion failed")
			os.Exit(4)
		}
		_, _ = fmt.Fprintln(os.Stdout, "deleted")
		os.Exit(0)
	default:
		_, _ = fmt.Fprintln(os.Stderr, "credential helper action invalid")
		os.Exit(5)
	}
}

func runCredentialStoreHelper(t *testing.T, path, action string) string {
	t.Helper()
	command := credentialStoreHelperCommand(path, action)
	output, err := command.Output()
	if err != nil {
		exitError, _ := err.(*exec.ExitError)
		if exitError != nil {
			t.Fatalf(
				"credential helper action %q failed: %q",
				action,
				exitError.Stderr,
			)
		}
		t.Fatalf("credential helper action %q failed: %v", action, err)
	}
	return strings.TrimSpace(string(output))
}

func runInterruptedCredentialStoreHelper(t *testing.T, path string) string {
	t.Helper()
	command := credentialStoreHelperCommand(path, "load-or-create-and-wait")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("credential helper StdoutPipe() error = %v", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("credential helper Start() error = %v", err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("credential helper public identity read failed: %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		_ = command.Wait()
		t.Fatalf("credential helper interruption failed: %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("interrupted credential helper exited successfully")
	}
	if stderr.Len() != 0 {
		t.Fatalf("interrupted credential helper stderr = %q", stderr.String())
	}
	return strings.TrimSpace(line)
}

func credentialStoreHelperCommand(path, action string) *exec.Cmd {
	acceptanceRoot := filepath.Dir(filepath.Dir(path))
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestCredentialStoreProcessHelper$",
	)
	command.Env = append(
		environmentWithoutCredentialStoreHelper(),
		credentialStoreProcessHelperEnvironment+"=1",
		credentialStoreProcessActionEnvironment+"="+action,
		acceptanceCredentialFileEnvironment+"="+path,
		"HOME="+filepath.Join(acceptanceRoot, "process-home"),
		"XDG_CACHE_HOME="+filepath.Join(acceptanceRoot, "process-cache"),
	)
	return command
}

func environmentWithoutCredentialStoreHelper() []string {
	excluded := map[string]struct{}{
		credentialStoreProcessHelperEnvironment: {},
		credentialStoreProcessActionEnvironment: {},
		acceptanceCredentialFileEnvironment:     {},
		"HOME":                                  {},
		"XDG_CACHE_HOME":                        {},
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
