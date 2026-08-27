package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type a2aFixtureInvocation struct {
	Arguments   []string `json:"arguments"`
	Input       string   `json:"input"`
	Environment string   `json:"environment"`
}

type a2aProcessResult struct {
	stdout   string
	stderr   string
	exitCode int
}

type a2aProcessRecord struct {
	PID       int `json:"pid"`
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid,omitempty"`
}

func TestActualA2ADispatchMatchesDirectSidecar(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		input     string
		exitCode  int
	}{
		{name: "root help", arguments: []string{"--help"}},
		{
			name:      "current send surface",
			arguments: []string{"--agent-card", "https://agent.example/card.json", "--output", "json", "send", "", "héllo world"},
			input:     "request with spaces",
			exitCode:  23,
		},
		{
			name:      "sidecar global flag collision",
			arguments: []string{"--agent", "sidecar-agent", "--version", "-v"},
			exitCode:  7,
		},
		{
			name:      "future upstream surface",
			arguments: []string{"future-upstream-command", "--new-flag", "value with spaces"},
			exitCode:  17,
		},
	}

	for _, managed := range []bool{false, true} {
		layout := "developer"
		if managed {
			layout = "managed"
		}
		t.Run(layout, func(t *testing.T) {
			root := buildA2ATestPair(t, managed)
			kado := filepath.Join(root, kadoTestBinaryName())
			sidecar := filepath.Join(root, a2aTestBinaryName())
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					environment := a2aFixtureEnvironment(test.exitCode)
					direct := runA2ATestProcess(t, sidecar, environment, test.input, test.arguments...)
					delegatedArguments := append([]string{"a2a"}, test.arguments...)
					delegated := runA2ATestProcess(t, kado, environment, test.input, delegatedArguments...)
					if direct != delegated {
						t.Fatalf("direct=%+v delegated=%+v", direct, delegated)
					}
					if delegated.exitCode != test.exitCode || delegated.stderr != "fixture-stderr\n" {
						t.Fatalf("result=%+v, want exit=%d and fixture stderr", delegated, test.exitCode)
					}
					var invocation a2aFixtureInvocation
					if err := json.Unmarshal([]byte(delegated.stdout), &invocation); err != nil {
						t.Fatalf("decode stdout %q: %v", delegated.stdout, err)
					}
					if !reflect.DeepEqual(invocation.Arguments, test.arguments) ||
						invocation.Input != test.input || invocation.Environment != "preserved ü" {
						t.Fatalf("invocation=%+v, want arguments=%q input=%q environment preserved", invocation, test.arguments, test.input)
					}
				})
			}
			if managed {
				active := filepath.Join(root, kadoTestBinaryName()+".d", "versions", "9.8.7")
				for _, name := range []string{kadoTestBinaryName(), a2aTestBinaryName()} {
					if info, err := os.Stat(filepath.Join(active, name)); err != nil || !info.Mode().IsRegular() {
						t.Fatalf("activated pair member %q is unavailable: %v", name, err)
					}
				}
			}
		})
	}
}

func TestActualA2AHelpAliasesAndKadoRootHelp(t *testing.T) {
	for _, managed := range []bool{false, true} {
		layout := "developer"
		if managed {
			layout = "managed"
		}
		t.Run(layout, func(t *testing.T) {
			root := buildA2ATestPair(t, managed)
			kado := filepath.Join(root, kadoTestBinaryName())
			sidecar := filepath.Join(root, a2aTestBinaryName())
			environment := a2aFixtureEnvironment(0)
			for _, test := range []struct {
				name      string
				direct    []string
				delegated []string
			}{
				{name: "direct root help", direct: []string{"--help"}, delegated: []string{"a2a", "--help"}},
				{name: "root help alias", direct: []string{"--help"}, delegated: []string{"help", "a2a"}},
				{name: "nested help alias", direct: []string{"help", "send"}, delegated: []string{"help", "a2a", "send"}},
				{
					name:      "leading Kado agent",
					direct:    []string{"help", "task", "get"},
					delegated: []string{"--agent", "codex", "help", "a2a", "task", "get"},
				},
			} {
				t.Run(test.name, func(t *testing.T) {
					direct := runA2ATestProcess(t, sidecar, environment, "", test.direct...)
					delegated := runA2ATestProcess(t, kado, environment, "", test.delegated...)
					if direct != delegated {
						t.Fatalf("direct=%+v delegated=%+v", direct, delegated)
					}
				})
			}

			rootHelp := runA2ATestProcess(t, kado, environment, "", "help")
			if rootHelp.exitCode != 0 || rootHelp.stderr != "" ||
				strings.Count(rootHelp.stdout, "\n  a2a              A2A CLI\n") != 1 ||
				strings.Contains(rootHelp.stdout, "kado-a2a") ||
				strings.Contains(rootHelp.stdout, `"arguments"`) {
				t.Fatalf("unexpected Kado help: %+v", rootHelp)
			}
		})
	}
}

func TestActualA2ANegativeNamespacesNeverExecuteTheSidecar(t *testing.T) {
	root := buildA2ATestPair(t, false)
	kado := filepath.Join(root, kadoTestBinaryName())
	for _, test := range []struct {
		name      string
		arguments []string
	}{
		{name: "old use namespace", arguments: []string{"use", "send", "hello"}},
		{name: "misplaced namespace", arguments: []string{"help", "search", "a2a"}},
		{name: "incomplete agent", arguments: []string{"--agent"}},
		{name: "empty agent", arguments: []string{"--agent=", "a2a"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "executed")
			environment := append(a2aFixtureEnvironment(0), "KADO_A2A_FIXTURE_MARKER="+marker)
			result := runA2ATestProcess(t, kado, environment, "", test.arguments...)
			if result.exitCode == 0 || strings.Contains(result.stdout, `"arguments"`) {
				t.Fatalf("unexpected result: %+v", result)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("sidecar execution marker exists: %v", err)
			}
		})
	}
}

func TestActualA2AInvalidSiblingFailsBeforeExecution(t *testing.T) {
	root := buildA2ATestPair(t, false)
	kado := filepath.Join(root, kadoTestBinaryName())
	sidecar := filepath.Join(root, a2aTestBinaryName())
	original, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "valid-sidecar-copy"+a2aTestBinarySuffix())
	if err := os.WriteFile(target, original, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "missing", setup: func(t *testing.T) {}},
		{name: "wrong size", setup: func(t *testing.T) {
			if err := os.WriteFile(sidecar, append(append([]byte{}, original...), 0), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "same size digest mismatch", setup: func(t *testing.T) {
			changed := append([]byte{}, original...)
			changed[len(changed)-1] ^= 0xff
			if err := os.WriteFile(sidecar, changed, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "nonregular", setup: func(t *testing.T) {
			if err := os.Mkdir(sidecar, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", setup: func(t *testing.T) {
			if err := os.Symlink(target, sidecar); err != nil {
				t.Skipf("sidecar symlinks are unavailable: %v", err)
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.RemoveAll(sidecar); err != nil {
				t.Fatal(err)
			}
			test.setup(t)
			marker := filepath.Join(t.TempDir(), "executed")
			environment := append(a2aFixtureEnvironment(0), "KADO_A2A_FIXTURE_MARKER="+marker)
			result := runA2ATestProcess(t, kado, environment, "", "a2a", "future-command")
			if result.exitCode != 1 || result.stdout != "" ||
				result.stderr != "kado: bundled A2A CLI is unavailable [a2a_unavailable]\n" {
				t.Fatalf("unexpected result: %+v", result)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("sidecar execution marker exists: %v", err)
			}
		})
	}
}

func TestActualA2ADoesNotSearchPathCurrentDirectoryOrEnvironment(t *testing.T) {
	root := buildA2ATestPair(t, false)
	kado := filepath.Join(root, kadoTestBinaryName())
	sibling := filepath.Join(root, a2aTestBinaryName())
	alternative := t.TempDir()
	alternativeSidecar := filepath.Join(alternative, a2aTestBinaryName())
	value, err := os.ReadFile(sibling)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alternativeSidecar, value, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sibling); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "executed")
	environment := append(
		a2aFixtureEnvironment(0),
		"KADO_A2A_FIXTURE_MARKER="+marker,
		"KADO_A2A_PATH="+alternativeSidecar,
		"PATH="+alternative+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	command := exec.Command(kado, "a2a", "future-command")
	command.Dir = alternative
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 1 || stdout.Len() != 0 ||
		stderr.String() != "kado: bundled A2A CLI is unavailable [a2a_unavailable]\n" {
		t.Fatalf("error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("alternative sidecar execution marker exists: %v", err)
	}
}

func TestActualPackageOwnedPathDelegatesAndRefusesLifecycle(t *testing.T) {
	channel := "homebrew"
	updateCommand := "brew upgrade kado"
	uninstallCommand := "brew uninstall kado"
	if runtime.GOOS == "windows" {
		channel = "scoop"
		updateCommand = "scoop update kado"
		uninstallCommand = "scoop uninstall kado"
	}

	built := buildA2ATestPairWithLDFlags(
		t,
		false,
		"-X github.com/kado-so/search/internal/buildinfo.InstallChannel="+channel,
	)
	packageRoot := filepath.Join(t.TempDir(), "Kado package ü", channel, "1.2.3", "libexec")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{kadoTestBinaryName(), a2aTestBinaryName()} {
		value, err := os.ReadFile(filepath.Join(built, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packageRoot, name), value, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	publicRoot := filepath.Join(t.TempDir(), "Kado public ü")
	if err := os.MkdirAll(publicRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	publicKado := filepath.Join(publicRoot, kadoTestBinaryName())
	if runtime.GOOS == "windows" {
		aliasRoot := filepath.Join(publicRoot, "current")
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", aliasRoot, packageRoot).CombinedOutput(); err != nil {
			t.Fatalf("create package junction: %v output=%q", err, output)
		}
		publicKado = filepath.Join(aliasRoot, kadoTestBinaryName())
	} else if err := os.Symlink(filepath.Join(packageRoot, kadoTestBinaryName()), publicKado); err != nil {
		t.Fatalf("create public Kado link: %v", err)
	}

	delegated := runA2ATestProcess(t, publicKado, a2aFixtureEnvironment(0), "", "a2a", "--output", "json", "version")
	if delegated.exitCode != 0 || delegated.stderr != "fixture-stderr\n" {
		t.Fatalf("delegated result = %+v", delegated)
	}
	before := make(map[string][32]byte, 2)
	for _, name := range []string{kadoTestBinaryName(), a2aTestBinaryName()} {
		value, err := os.ReadFile(filepath.Join(packageRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = sha256.Sum256(value)
	}
	for _, operation := range []struct {
		arguments []string
		command   string
	}{
		{arguments: []string{"update", "--dry-run"}, command: updateCommand},
		{arguments: []string{"uninstall", "--yes", "--purge-credentials"}, command: uninstallCommand},
	} {
		result := runA2ATestProcess(t, publicKado, a2aFixtureEnvironment(0), "", operation.arguments...)
		if result.exitCode != 1 || result.stdout != "" || !strings.Contains(result.stderr, operation.command) {
			t.Fatalf("Run(%q) = %+v", operation.arguments, result)
		}
	}
	for name, digest := range before {
		value, err := os.ReadFile(filepath.Join(packageRoot, name))
		if err != nil || sha256.Sum256(value) != digest {
			t.Fatalf("package member %s changed: %v", name, err)
		}
	}
}

func buildA2ATestPair(t *testing.T, managed bool) string {
	t.Helper()
	return buildA2ATestPairWithLDFlags(t, managed, "")
}

func buildA2ATestPairWithLDFlags(t *testing.T, managed bool, extraLDFlags string) string {
	t.Helper()
	moduleRoot := cliModuleRoot(t)
	root := t.TempDir()
	if managed {
		var err error
		root, err = os.MkdirTemp(moduleRoot, ".a2a-dispatch-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}
	sidecar := filepath.Join(root, a2aTestBinaryName())
	buildA2ATestBinary(t, moduleRoot, "build", "-trimpath", "-buildvcs=false", "-o", sidecar, "./cmd/kado/testdata/a2a-sidecar")
	value, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(value)
	ldflags := fmt.Sprintf(
		"-X github.com/kado-so/search/internal/buildinfo.A2AArtifactSHA256=%x "+
			"-X github.com/kado-so/search/internal/buildinfo.A2AArtifactSize=%d",
		digest,
		len(value),
	)
	if managed {
		ldflags += " -X github.com/kado-so/search/internal/buildinfo.Version=9.8.7" +
			" -X github.com/kado-so/search/internal/buildinfo.InstallChannel=direct"
	}
	if extraLDFlags != "" {
		ldflags += " " + extraLDFlags
	}
	kado := filepath.Join(root, kadoTestBinaryName())
	buildA2ATestBinary(t, moduleRoot, "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", kado, "./cmd/kado")
	return root
}

func buildA2ATestBinary(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("go", arguments...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go %v error = %v output=%q", arguments, err, output)
	}
}

func runA2ATestProcess(t *testing.T, path string, environment []string, input string, arguments ...string) a2aProcessResult {
	t.Helper()
	command := exec.Command(path, arguments...)
	command.Env = environment
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		exitError, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %q error = %v", arguments, err)
		}
		code = exitError.ExitCode()
	}
	return a2aProcessResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: code}
}

func a2aFixtureEnvironment(exitCode int) []string {
	return append(
		environmentWithout(
			"KADO_A2A_FIXTURE_EXIT",
			"KADO_A2A_FIXTURE_MARKER",
			"KADO_A2A_FIXTURE_VALUE",
			"KADO_LAUNCHER_PATH",
			"KADO_PAYLOAD_PATH",
		),
		"KADO_A2A_FIXTURE_EXIT="+strconv.Itoa(exitCode),
		"KADO_A2A_FIXTURE_VALUE=preserved ü",
	)
}

func kadoTestBinaryName() string {
	if runtime.GOOS == "windows" {
		return "kado.exe"
	}
	return "kado"
}

func a2aTestBinaryName() string { return "kado-a2a" + a2aTestBinarySuffix() }

func a2aTestBinarySuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func waitA2AProcessRecord(t *testing.T, path string) a2aProcessRecord {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		value, err := os.ReadFile(path)
		if err == nil {
			var record a2aProcessRecord
			if json.Unmarshal(value, &record) == nil && record.PID > 0 && record.ParentPID > 0 {
				return record
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process record %q was not written", path)
	return a2aProcessRecord{}
}

func processDescription(record a2aProcessRecord) string {
	return fmt.Sprintf("sidecar=%d parent=%d child=%d", record.PID, record.ParentPID, record.ChildPID)
}
