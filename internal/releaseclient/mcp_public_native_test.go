package releaseclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kado-so/search/internal/launcher"
	"github.com/kado-so/search/internal/payload"
)

func TestPublicMCPCompleteBundle(t *testing.T) {
	directory := os.Getenv("KADO_MCP_QUALIFICATION_BUNDLE")
	if directory == "" {
		t.Skip("supply native MCP qualification bundle")
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(filepath.Join(directory, "payload.gen.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Files []payload.File `json:"files"`
	}
	if err := json.Unmarshal(encoded, &source); err != nil {
		t.Fatal(err)
	}
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	f := completeRelease(t, "9.0.0", key)
	var metadata Metadata
	if err := json.Unmarshal(f.fetch[f.metadataURL], &metadata); err != nil {
		t.Fatal(err)
	}
	candidate := buildStampedExecutable(t, metadata, f.publicKey)
	b, err := VerifyCompleteTarget(f.target, f.fetch[f.target.Archive.URL], key.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range source.Files {
		v, err := payload.ReadFile(directory, file.Path, file.Size)
		if err != nil || payload.Digest(v) != file.SHA256 {
			t.Fatalf("component changed: %s: %v", file.Path, err)
		}
		b.Files["mcp/"+file.Path] = v
	}
	b.Files[b.Manifest.Entries["kado"]], err = os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	b, err = payload.Seal(b.Manifest, b.Files, key)
	if err != nil {
		t.Fatal(err)
	}
	_, format, _ := targetLayout(runtime.GOOS)
	archive, err := payload.Archive(b, format, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	f.target.Archive.Size, f.target.Archive.SHA256 = int64(len(archive)), Digest(archive)
	f.target.Payload = &EmbeddedArtifact{Size: int64(len(b.Encoded)), SHA256: Digest(b.Encoded)}
	for i, target := range metadata.Targets {
		if target.OS == runtime.GOOS && target.Arch == runtime.GOARCH {
			metadata.Targets[i] = f.target
		}
	}
	metadataBytes, err := CanonicalMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	download := filepath.Join(root, "download")
	install := filepath.Join(root, "install space ü")
	for _, d := range []string{download, install} {
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string][]byte{"release-metadata.json": metadataBytes, "release-metadata.json.sig": ed25519.Sign(key, metadataBytes), f.target.Archive.Name: archive} {
		if err := os.WriteFile(filepath.Join(download, name), value, 0600); err != nil {
			t.Fatal(err)
		}
	}
	home := filepath.Join(root, "state")
	os.Mkdir(home, 0700)
	os.WriteFile(filepath.Join(home, "credential-sentinel"), []byte("preserved"), 0600)
	env := append(payload.NodeEnvironment(os.Environ()), "KADO_MCP_HOME_DIR="+home, "KADO_MAINTENANCE_CHILD=1", "NO_COLOR=1")
	type result struct {
		stdout, stderr string
		code           int
	}
	run := func(executable, input string, args ...string) result {
		t.Helper()
		timeout := 90 * time.Second
		if len(args) > 0 && args[0] == "__install-bundle" {
			// Match exact-candidate qualification: extracting and verifying the
			// complete runtime takes longer on native Windows runners.
			timeout = 3 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		c := exec.CommandContext(ctx, executable, args...)
		c.Env = env
		c.Stdin = strings.NewReader(input)
		var out, errors bytes.Buffer
		c.Stdout, c.Stderr = &out, &errors
		err := c.Run()
		if ctx.Err() != nil {
			t.Fatalf("command %q exceeded %s: %v", args, timeout, ctx.Err())
		}
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		return result{out.String(), errors.String(), code}
	}
	kado := filepath.Join(install, executableName(runtime.GOOS))
	if r := run(candidate, "", "__install-bundle", "--directory", download, "--target", kado); r.code != 0 {
		t.Fatalf("fresh installer: %+v", r)
	}
	active, err := launcher.ActiveComplete(kado, key.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := os.Stat(kado); err == nil {
			_ = launcher.UninstallComplete(kado, key.Public().(ed25519.PublicKey))
		}
	}()
	for _, args := range [][]string{{"--help"}, {"tools-call", "--help"}, {"--version", "--json"}, {"future-command", "--future-flag", "", "ü words"}} {
		direct := run(active.Entry("node"), "", append([]string{"--no-global-search-paths", active.Entry("mcp")}, args...)...)
		delegated := run(kado, "", append([]string{"mcp"}, args...)...)
		if direct != delegated {
			t.Fatalf("parity %q: direct=%+v public=%+v", args, direct, delegated)
		}
	}
	for _, args := range [][]string{{"mcp", "--help"}, {"help", "mcp"}, {"help", "mcp", "tools-call"}, {"help", "mcp", "connect"}} {
		r := run(kado, "", args...)
		if r.code != 0 || !strings.Contains(r.stdout, "kado mcp") || strings.Contains(r.stdout, "kado-mcp") || strings.Contains(strings.ToLower(r.stdout), "apify") {
			t.Fatalf("help identity: %+v", r)
		}
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		if r := run(kado, "", "completion", shell); r.code != 0 || r.stdout == "" {
			t.Fatalf("shell %s: %+v", shell, r)
		}
	}
	if r := run(kado, "", "__complete", "mcp", "tools-"); r.code != 0 || !strings.Contains(r.stdout, "tools-call") || r.stderr != "" {
		t.Fatalf("completion: %+v", r)
	}
	if tutorial := os.Getenv("KADO_MCP_TUTORIAL_SCRIPT"); tutorial != "" {
		if r := run(active.Entry("node"), "", tutorial, kado); r.code != 0 {
			t.Fatalf("installed MCP tutorial: %+v", r)
		} else {
			t.Log(r.stdout)
		}
	}
	// Even completion must fail closed if any required component changes.
	cliPath := active.Entry("mcp")
	cliBytes, err := os.ReadFile(cliPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, missing := range []bool{false, true} {
		if missing {
			err = os.Remove(cliPath)
		} else {
			err = os.WriteFile(cliPath, []byte("tampered"), 0644)
		}
		if err != nil {
			t.Fatal(err)
		}
		completion := run(kado, "", "__complete", "mcp", "tools-")
		ordinary := run(kado, "", "mcp", "--help")
		if err := os.WriteFile(cliPath, cliBytes, 0644); err != nil {
			t.Fatal(err)
		}
		if completion.code != 0 || completion.stdout != ":1\n" || completion.stderr != "" || ordinary.code == 0 || ordinary.stdout != "" {
			t.Fatalf("invalid component missing=%v: completion=%+v ordinary=%+v", missing, completion, ordinary)
		}
	}
	// Actual filesystem aliases must resolve to the signed physical payload.
	alias := filepath.Join(root, "alias space")
	if runtime.GOOS == "windows" {
		c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `New-Item -ItemType Junction -Path $env:KADO_TEST_ALIAS -Target $env:KADO_TEST_TARGET -ErrorAction Stop | Out-Null`)
		c.Env = append(env, "KADO_TEST_ALIAS="+alias, "KADO_TEST_TARGET="+install)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("junction: %v %s", err, out)
		}
	} else if err := os.Symlink(install, alias); err != nil {
		t.Fatal(err)
	}
	if r := run(filepath.Join(alias, executableName(runtime.GOOS)), "", "mcp", "--help"); r.code != 0 || !strings.Contains(r.stdout, "kado mcp") {
		t.Fatalf("alias: %+v", r)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	// Model the manager's installed directory and public shim target. The
	// immutable payload is closed; the parent remains owned by the manager.
	channel := "homebrew"
	if runtime.GOOS == "windows" {
		channel = "scoop"
	}
	packageExecutable := buildStampedExecutable(t, metadata, f.publicKey, channel)
	packageFiles := make(map[string][]byte, len(b.Files)+1)
	for name, value := range b.Files {
		packageFiles[name] = value
	}
	packageFiles[b.Manifest.Entries["kado"]], err = os.ReadFile(packageExecutable)
	if err != nil {
		t.Fatal(err)
	}
	packageFiles["kado.install.json"] = []byte("{\"schema_version\":1,\"channel\":\"" + channel + "\"}\n")
	packaged, err := payload.Seal(b.Manifest, packageFiles, key)
	if err != nil {
		t.Fatal(err)
	}
	packageParent := filepath.Join(root, "package")
	if err := os.Mkdir(packageParent, 0755); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(packageParent, "payload")
	if err := os.Mkdir(packageRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := payload.WriteTree(packageRoot, packaged, key.Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageParent, "install.json"), []byte("manager metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	packageKado := filepath.Join(packageRoot, executableName(runtime.GOOS))
	if r := run(packageKado, "", "mcp", "--help"); r.code != 0 || !strings.Contains(r.stdout, "kado mcp") {
		t.Fatalf("package invocation: %+v", r)
	}
	for _, args := range [][]string{{"update"}, {"uninstall", "--yes"}} {
		if r := run(packageKado, "", args...); r.code == 0 || !strings.Contains(strings.ToLower(r.stderr), channel) {
			t.Fatalf("package ownership: %+v", r)
		}
	}
	configuration := filepath.Join(root, "servers.json")
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fixture": map[string]any{"command": active.Entry("node"), "args": []string{filepath.Join(active.Root, "mcp/app/qualification/fixture.mjs")}}}})
	if err := os.WriteFile(configuration, config, 0600); err != nil {
		t.Fatal(err)
	}
	if r := run(kado, "", "mcp", "connect", configuration+":fixture", "@g9", "--no-profile", "--json"); r.code != 0 {
		t.Fatalf("connect: %+v", r)
	}
	if r := run(kado, "{\"text\":\"piped Unicode ü\"}", "mcp", "@g9", "tools-call", "echo", "--stdin", "--json"); r.code != 0 || !strings.Contains(r.stdout, "piped Unicode ü") {
		t.Fatalf("session/input: %+v", r)
	}
	r := run(kado, "", "uninstall", "--yes")
	if r.code != 0 {
		t.Fatalf("uninstall: %+v", r)
	}
	if runtime.GOOS == "windows" {
		resultPath := strings.TrimSpace(strings.TrimPrefix(r.stdout, "uninstall pending; result: "))
		deadline := time.Now().Add(90 * time.Second)
		for {
			encoded, readErr := os.ReadFile(resultPath)
			if readErr == nil {
				var status struct {
					Status string `json:"status"`
				}
				if json.Unmarshal(encoded, &status) != nil || status.Status != "success" {
					t.Fatalf("uninstall result: %s", encoded)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("uninstall result unavailable: %v (%s)", readErr, resultPath)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if _, err := os.Stat(kado); !os.IsNotExist(err) {
		t.Fatalf("uninstall left launcher: %v", err)
	}
	if value, err := os.ReadFile(filepath.Join(home, "credential-sentinel")); err != nil || string(value) != "preserved" {
		t.Fatal("credentials changed")
	}
}
