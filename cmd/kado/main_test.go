package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestActualCLIUsesConfiguredFileCredentialBackend(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(TempDir()) error = %v", err)
	}
	configDirectory := filepath.Join(root, "config")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(config) error = %v", err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	configBytes := []byte(`{"credentials":{"backend":"file"}}` + "\n")
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
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
		),
		"KADO_BASE_URL=https://127.0.0.1:1",
		"KADO_CONFIG_DIR="+configDirectory,
	)

	stdout, stderr, exitCode := runActualCLI(
		t,
		binaryPath,
		environment,
		"--agent",
		"default",
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
	for _, path := range []string{
		filepath.Join(configDirectory, "host.json"),
		filepath.Join(configDirectory, "secrets", "default"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected CLI state %q: %v", path, err)
		}
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
