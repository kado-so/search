package releaseclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// Qualify the production builder's exact archive without resealing components
// or replacing A2A. The bootstrap's embedded verifier authenticates installation.
func TestExactMCPReleaseCandidate(t *testing.T) {
	binary, directory := os.Getenv("KADO_COMPLETE_CANDIDATE_BINARY"), os.Getenv("KADO_COMPLETE_CANDIDATE_RELEASE")
	if binary == "" {
		t.Skip("supply the native signed complete candidate and release directory")
	}
	for _, path := range []string{binary, directory} {
		if !filepath.IsAbs(path) {
			t.Fatal("absolute candidate paths required")
		}
	}
	keyPEM, err := os.ReadFile(filepath.Join(directory, "release-public-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		t.Fatal("release verifier PEM missing")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatal("unexpected verifier key type")
	}
	root := t.TempDir()
	install := filepath.Join(root, "install space ü")
	if err := os.Mkdir(install, 0700); err != nil {
		t.Fatal(err)
	}
	env := append(payload.NodeEnvironment(os.Environ()), "KADO_MAINTENANCE_CHILD=1", "NO_COLOR=1", "KADO_MCP_HOME_DIR="+filepath.Join(root, "profiles"))
	type commandResult struct {
		output, errors []byte
		err            error
	}
	run := func(executable string, args ...string) commandResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, executable, args...)
		command.Env = env
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		return commandResult{stdout.Bytes(), stderr.Bytes(), err}
	}
	require := func(executable string, args ...string) []byte {
		t.Helper()
		result := run(executable, args...)
		if result.err != nil {
			t.Fatalf("%v: %v\n%s", args, result.err, result.errors)
		}
		return result.output
	}
	kado := filepath.Join(install, executableName(runtime.GOOS))
	require(binary, "__install-bundle", "--directory", directory, "--target", kado)
	defer func() {
		if err := launcher.UninstallComplete(kado, key); err != nil {
			t.Errorf("candidate cleanup: %v", err)
		}
	}()
	active, err := launcher.ActiveComplete(kado, key)
	if err != nil {
		t.Fatal(err)
	}
	if active.Manifest.Target != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatal("candidate is not native to this runner")
	}
	require(kado, "release", "verify", "--directory", directory)
	for _, args := range [][]string{{"version", "--json"}, {"a2a", "--output", "json", "version"}, {"mcp", "--version", "--json"}} {
		if output := require(kado, args...); !json.Valid(output) {
			t.Fatalf("invalid version JSON: %s", output)
		}
	}
	for _, args := range [][]string{{"mcp", "--help"}, {"help", "mcp", "tools-call"}} {
		output := string(require(kado, args...))
		if !strings.Contains(output, "kado mcp") || strings.Contains(strings.ToLower(output), "apify") {
			t.Fatal("public MCP identity changed")
		}
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		if len(require(kado, "completion", shell)) == 0 {
			t.Fatal("empty completion")
		}
	}
	// Empty PATH proves dispatch uses only the signed private Node/runtime.
	originalEnvironment := env
	env = append(append([]string{}, env...), "PATH=")
	require(kado, "mcp", "--version", "--json")
	env = originalEnvironment
	tutorial, err := filepath.Abs(filepath.Join("..", "..", "tools", "test", "mcp-tutorial.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(require(active.Entry("node"), tutorial, kado)))
	// Corrupt a required component, then restore the exact bytes as a local
	// repair drill. Neither invocation nor completion may execute corrupt code.
	entry := active.Entry("mcp")
	original, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	ordinary := run(kado, "mcp", "--help")
	completion := run(kado, "__complete", "mcp", "tools-")
	if err := os.WriteFile(entry, original, 0644); err != nil {
		t.Fatal(err)
	}
	if ordinary.err == nil || len(ordinary.output) != 0 || completion.err != nil || string(completion.output) != ":1\n" || len(completion.errors) != 0 {
		t.Fatal("tampered component did not fail closed")
	}
	require(kado, "mcp", "--version", "--json")
	// Exercise the actual executable's four embedded skills with an unavailable
	// catalog, keeping every destination and receipt under the disposable home.
	home := filepath.Join(root, "skill-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	unavailableCatalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer unavailableCatalog.Close()
	beforeSkills := env
	env = append(append([]string{}, env...), "HOME="+home, "USERPROFILE="+home, "KADO_CONFIG_DIR="+filepath.Join(root, "skill-config"), "HTTPS_PROXY="+unavailableCatalog.URL, "HTTP_PROXY="+unavailableCatalog.URL, "NO_PROXY=")
	require(kado, "skill", "install", "--agent", "codex")
	for _, name := range []string{"kado-search", "kado-cli-non-search", "kado-a2a", "kado-mcp"} {
		if data, err := os.ReadFile(filepath.Join(home, ".agents", "skills", name, "SKILL.md")); err != nil || len(data) == 0 {
			t.Fatalf("embedded %s skill not installed: %v", name, err)
		}
	}
	require(kado, "skill", "uninstall", "--all")
	env = beforeSkills
	t.Log("all four embedded skills installed offline and uninstalled from the isolated home")
	if future := os.Getenv("KADO_COMPLETE_CANDIDATE_FUTURE"); future != "" {
		if !filepath.IsAbs(future) {
			t.Fatal("absolute future candidate directory required")
		}
		manager := func(dir string) Manager {
			return Manager{MetadataURL: "https://kado.so/install/releases/stable/release-metadata.json", PublicKey: base64.RawStdEncoding.EncodeToString(key), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Fetcher: exactCandidateFiles(dir)}
		}
		options := Options{TargetPath: kado, LauncherPath: kado, CurrentVersion: active.Manifest.Version}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, err := manager(future).Update(ctx, options); err != nil {
			t.Fatal("exact candidate upgrade", err)
		}
		upgraded, err := launcher.ActiveComplete(kado, key)
		if err != nil || upgraded.Manifest.Version == active.Manifest.Version {
			t.Fatal("future complete payload was not activated", err)
		}
		require(kado, "mcp", "--version", "--json")
		require(kado, "a2a", "--output", "json", "version")
		if err := os.WriteFile(upgraded.Entry("mcp"), []byte("corrupt"), 0644); err != nil {
			t.Fatal(err)
		}
		options.CurrentVersion = upgraded.Manifest.Version
		if _, err := manager(future).Update(ctx, options); err != nil {
			t.Fatal("exact candidate repair", err)
		}
		require(kado, "mcp", "--version", "--json")
		if _, err := manager(directory).Update(ctx, options); !errors.Is(err, ErrDowngrade) {
			t.Fatal("rollback requires explicit downgrade option", err)
		}
		options.AllowDowngrade = true
		if _, err := manager(directory).Update(ctx, options); err != nil {
			t.Fatal("exact candidate rollback", err)
		}
		rolledBack, err := launcher.ActiveComplete(kado, key)
		if err != nil || rolledBack.Digest != active.Digest {
			t.Fatal("rollback did not restore exact original payload", err)
		}
		require(kado, "mcp", "--version", "--json")
		t.Log("exact signed future upgrade, same-version repair and explicit rollback passed")
	}
	t.Logf("exact signed candidate passed on %s/%s; payload=%s", runtime.GOOS, runtime.GOARCH, active.Digest)
}

// Replace delivery only: retain the production signature, candidate executable
// verifier, archive verification, maintenance protocol and activation manager.
type exactCandidateFiles string

func (directory exactCandidateFiles) Fetch(_ context.Context, rawURL string, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host != "kado.so" {
		return nil, errors.New("unexpected candidate asset origin")
	}
	f, err := os.Open(filepath.Join(string(directory), filepath.Base(u.Path)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("candidate asset exceeds limit")
	}
	return data, nil
}
