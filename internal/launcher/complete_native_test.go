package launcher

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kado-so/search/internal/payload"
)

// CI supplies the already-qualified native MCP payload. This is deliberately
// separate from the deterministic maintenance-protocol fake in complete_test.
func TestCompleteNativeMCP(t *testing.T) {
	directory := os.Getenv("KADO_MCP_QUALIFICATION_BUNDLE")
	if directory == "" {
		t.Skip("native matrix supplies the packaged MCP component")
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(filepath.Join(directory, "payload.gen.json"))
	if err != nil {
		t.Fatal(err)
	}
	var component struct {
		Files []payload.File `json:"files"`
	}
	if err := json.Unmarshal(encoded, &component); err != nil {
		t.Fatal(err)
	}
	one, key := completeFixture(t, "8.0.0")
	for _, f := range component.Files {
		v, err := payload.ReadFile(directory, f.Path, f.Size)
		if err != nil || payload.Digest(v) != f.SHA256 {
			t.Fatalf("qualified component changed: %s: %v", f.Path, err)
		}
		one.Files["mcp/"+f.Path] = v
	}
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{19}, 32))
	one, err = payload.Seal(one.Manifest, one.Files, private)
	if err != nil {
		t.Fatal(err)
	}
	launcher := completeInstallation(t)
	installComplete(t, launcher, one, key)
	first, err := ActiveComplete(launcher, key)
	if err != nil {
		t.Fatal(err)
	}
	stateRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	homes := []string{filepath.Join(stateRoot, "home-a"), filepath.Join(stateRoot, "home-b")}
	configuration := filepath.Join(stateRoot, "fixture.json")
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fixture": map[string]any{"command": first.Entry("node"), "args": []string{filepath.Join(first.Root, "mcp", "app", "qualification", "fixture.mjs")}}}})
	if err := os.WriteFile(configuration, config, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(p CompletePaths, home string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, p.Entry("node"), append([]string{"--no-global-search-paths", p.Entry("mcp")}, args...)...)
		cmd.Env = append(cleanNodeEnvironment(os.Environ()), "KADO_MCP_HOME_DIR="+home)
		configureMaintenanceProcess(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("MCP %v: %v\n%s", args, err, out)
		}
		return out
	}
	for _, home := range homes {
		if err := os.Mkdir(home, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "credential-sentinel"), []byte("preserve-latest-credentials"), 0600); err != nil {
			t.Fatal(err)
		}
		run(first, home, "connect", configuration+":fixture", "@g8-native", "--no-profile", "--json")
		defer func(home string) {
			// Best effort cleanup on an assertion failure, still through owned IPC.
			if _, err := os.Stat(first.Entry("node")); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, first.Entry("node"), first.Entry("mcp"), "@g8-native", "close", "--json")
				cmd.Env = append(cleanNodeEnvironment(os.Environ()), "KADO_MCP_HOME_DIR="+home)
				configureMaintenanceProcess(cmd)
				_ = cmd.Run()
			}
		}(home)
	}
	for _, version := range []string{"8.1.0", "8.2.0"} {
		m := one.Manifest
		m.Version = version
		b, err := payload.Seal(m, one.Files, private)
		if err != nil {
			t.Fatal(err)
		}
		installComplete(t, launcher, b, key)
	}
	active, err := ActiveComplete(launcher, key)
	if err != nil || active.Manifest.Version != "8.2.0" {
		t.Fatal("latest activation failed", err)
	}
	if _, err := os.Stat(first.Entry("node")); err != nil {
		t.Fatal("running session payload was pruned", err)
	}
	for _, home := range homes {
		out := run(active, home, "@g8-native", "tools-call", "echo", "{\"text\":\"session survived update\"}", "--json")
		if !strings.Contains(string(out), "session survived update") {
			t.Fatal("old session unusable after update")
		}
	}
	if err := UninstallComplete(launcher, key); err != nil {
		t.Fatal("native uninstall", err)
	}
	if _, err := os.Stat(launcher); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("launcher remains after uninstall", err)
	}
	for _, home := range homes {
		value, err := os.ReadFile(filepath.Join(home, "credential-sentinel"))
		if err != nil || string(value) != "preserve-latest-credentials" {
			t.Fatal("credentials changed during uninstall")
		}
		owners, err := os.ReadDir(filepath.Join(home, "session-owners-v1"))
		if err != nil {
			t.Fatal(err)
		}
		for _, owner := range owners {
			if strings.HasSuffix(owner.Name(), ".json") {
				t.Fatal("owned session remains after uninstall")
			}
		}
	}
}
