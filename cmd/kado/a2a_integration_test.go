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

func buildA2ATestPair(t *testing.T, managed bool) string {
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
