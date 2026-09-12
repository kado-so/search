package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This opt-in test consumes an independently built MCP payload. Search forwards
// opaque argv/stdio/exit status; the MCP component remains the sole command parser.
func TestMCPCommandBoundaryCandidate(t *testing.T) {
	root := os.Getenv("KADO_MCP_QUALIFICATION_BUNDLE")
	if root == "" {
		t.Skip("set KADO_MCP_QUALIFICATION_BUNDLE to the native G2/G3 payload")
	}
	digestBytes, err := os.ReadFile(root + ".manifest-sha256")
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.TrimSpace(string(digestBytes))
	m, err := verify(root, digest)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	harness := filepath.Join(directory, "probe")
	if runtime.GOOS == "windows" {
		harness += ".exe"
	}
	build := exec.Command("go", "build", "-o", harness, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	node := filepath.Join(root, filepath.FromSlash(m.Runtime))
	entry := filepath.Join(root, filepath.FromSlash(m.Entries["cli"]))
	type result struct {
		stdout, stderr string
		code           int
	}
	run := func(binary string, args []string, stdin string) result {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Dir = root
		command.Env = append(cleanEnvironment(os.Environ()), "KADO_MCP_HOME_DIR="+filepath.Join(directory, "state"), "NO_COLOR=1", "KADO_MCP_JSON=0")
		command.Stdin = strings.NewReader(stdin)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		return result{stdout.String(), stderr.String(), code}
	}
	for _, vector := range []struct {
		name  string
		args  []string
		stdin string
		code  int
	}{
		{"help", []string{"tools-call", "--help"}, "", 0},
		{"future-command", []string{"future-command", "--future-flag", "ü & $(literal)"}, "", 1},
		{"json-argv", []string{"tools-call", "https://example.invalid/mcp?x=a%26b&sig=%2B", "ü tool & name", "--args", "{invalid", "--json"}, "", 1},
		{"stdin", []string{"tools-call", "https://example.invalid/mcp", "echo", "--stdin", "--json"}, "[\"ü & literal\"]", 1},
	} {
		t.Run(vector.name, func(t *testing.T) {
			direct := run(node, append([]string{"--no-global-search-paths", entry}, vector.args...), vector.stdin)
			delegated := run(harness, append([]string{"-bundle", root, "-digest", digest, "-entry", "cli", "--"}, vector.args...), vector.stdin)
			if direct != delegated || delegated.code != vector.code {
				t.Fatalf("forwarding changed the result: direct=%+v delegated=%+v", direct, delegated)
			}
			if vector.name == "stdin" || vector.name == "json-argv" {
				var failure struct {
					Code int `json:"code"`
				}
				if delegated.stdout != "" || json.Unmarshal([]byte(delegated.stderr), &failure) != nil || failure.Code != 1 {
					t.Fatal("unclean JSON error boundary")
				}
			}
		})
	}
}
