package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	binaryPath := filepath.Join(root, kadoTestBinaryName())
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/kado")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/kado error = %v output=%q", err, output)
	}
	environment := append(
		environmentWithout(
			"KADO_CONFIG_DIR",
		),
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

func TestActualDirectLauncherBootstrapsAndForwardsExitCodes(t *testing.T) {
	moduleRoot := cliModuleRoot(t)
	root, err := os.MkdirTemp(moduleRoot, ".launcher-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binaryName := "kado"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(root, binaryName)
	build := exec.Command(
		"go", "build", "-trimpath", "-buildvcs=false",
		"-ldflags", "-X github.com/kado-so/search/internal/buildinfo.Version=1.0.0 -X github.com/kado-so/search/internal/buildinfo.InstallChannel=direct",
		"-o", binaryPath, "./cmd/kado",
	)
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build direct launcher error = %v output=%q", err, output)
	}
	stable, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	configDirectory := filepath.Join(root, "config")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := append(environmentWithout("KADO_CONFIG_DIR"), "KADO_CONFIG_DIR="+configDirectory)
	stdout, stderr, exitCode := runActualCLI(t, binaryPath, environment, "version", "--json")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, `"version":"1.0.0"`) {
		t.Fatalf("version exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	payload := filepath.Join(binaryPath+".d", "versions", "1.0.0", binaryName)
	for _, path := range []string{
		payload,
		filepath.Join(binaryPath+".d", "activations", "00000000000000000001.json"),
		filepath.Join(root, "kado.install.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("launcher path %q: %v", path, err)
		}
	}
	after, err := os.ReadFile(binaryPath)
	if err != nil || !bytes.Equal(stable, after) {
		t.Fatal("stable launcher changed during bootstrap")
	}

	const workers = 12
	var wait sync.WaitGroup
	results := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			command := exec.Command(binaryPath, "version", "--json")
			command.Env = environment
			output, err := command.CombinedOutput()
			if err == nil && !bytes.Contains(output, []byte(`"version":"1.0.0"`)) {
				err = errors.New("launcher returned the wrong version")
			}
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent launcher run: %v", err)
		}
	}
	_, stderr, exitCode = runActualCLI(t, binaryPath, environment, "search", "--timeout", "0", "smoke")
	if exitCode != 2 || !strings.Contains(stderr, "search timeout must be between") {
		t.Fatalf("search usage exit=%d stderr=%q", exitCode, stderr)
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
