package main

import (
	"crypto/ed25519"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kado-so/search/internal/payload"
	"github.com/kado-so/search/internal/releaseclient"
)

func releaseComponentFixture(t *testing.T, target buildTarget) (string, string, payload.Component) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := payload.Component{Version: "0.1.0-dev.0", Commit: strings.Repeat("a", 40), LockSHA256: strings.Repeat("b", 64), NodeVersion: "24.20.0", NodeArchiveSHA256: strings.Repeat("c", 64)}
	var files []payload.File
	for role, p := range payload.Entries(target.goos + "/" + target.goarch) {
		if role == "kado" || role == "a2a" {
			continue
		}
		p = strings.TrimPrefix(p, "mcp/")
		v := []byte("component-" + role)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(p))), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), v, 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, payload.File{Path: p, Size: int64(len(v)), SHA256: payload.Digest(v)})
	}
	dependency := "app/node_modules/example/package.json"
	packageJSON := []byte("{\"name\":\"example\",\"version\":\"1.0.0\",\"license\":\"MIT\"}\n")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(dependency))), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(dependency)), packageJSON, 0644); err != nil {
		t.Fatal(err)
	}
	files = append(files, payload.File{Path: dependency, Size: int64(len(packageJSON)), SHA256: payload.Digest(packageJSON)})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	// Component descriptors have no mode; the final signed outer manifest fixes
	// executable roles consistently across targets.
	var descriptors []map[string]any
	for _, f := range files {
		descriptors = append(descriptors, map[string]any{"path": f.Path, "size": f.Size, "sha256": f.SHA256})
	}
	platform := target.goos
	if platform == "windows" {
		platform = "win32"
	}
	arch := target.goarch
	if arch == "amd64" {
		arch = "x64"
	}
	runtimePath := "runtime/node"
	if target.goos == "windows" {
		runtimePath += ".exe"
	}
	m := map[string]any{"schema": 1, "purpose": "kado-release-component-v1", "target": platform + "-" + arch, "nodeVersion": c.NodeVersion, "runtime": runtimePath, "entries": map[string]string{"cli": "app/dist/cli/index.js", "bridge": "app/dist/bridge/index.js", "maintenance": "app/dist/maintenance/index.js"}, "packages": map[string]string{"example": "1.0.0"}, "component": c, "files": descriptors}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(dir, "payload.gen.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
	return dir, payload.Digest(b), c
}

func TestMCPComponentRequiresPinnedIdentityAndCompleteFiles(t *testing.T) {
	target := buildTarget{goos: runtime.GOOS, goarch: runtime.GOARCH}
	dir, digest, c := releaseComponentFixture(t, target)
	if _, _, err := readMCPComponent(dir, c.Commit, digest, target); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readMCPComponent(dir, strings.Repeat("d", 40), digest, target); err == nil {
		t.Fatal("accepted wrong source commit")
	}
	if _, _, err := readMCPComponent(dir, c.Commit, strings.Repeat("d", 64), target); err == nil {
		t.Fatal("accepted wrong independently pinned digest")
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "dist", "cli", "index.js"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readMCPComponent(dir, c.Commit, digest, target); err == nil {
		t.Fatal("accepted changed component file")
	}
}

func TestCompleteReleaseTargetAndSupplyChain(t *testing.T) {
	target := buildTarget{goos: runtime.GOOS, goarch: runtime.GOARCH}
	componentDir, digest, c := releaseComponentFixture(t, target)
	prebuilt := t.TempDir()
	// macOS temporary directories can have symlinked ancestors. Keep the moved
	// fixture canonical, as required by the component verifier.
	mcp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(componentDir, filepath.Join(mcp, target.goos+"-"+target.goarch)); err != nil {
		t.Fatal(err)
	}
	for _, binary := range []struct{ name, module string }{{"kado", "github.com/kado-so/search"}, {"kado-a2a", a2aModule}} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+binary.module+"\n\ngo 1.26.0\n\ntoolchain go1.26.4\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
		runTestCommand(t, dir, "go", "build", "-trimpath", "-o", filepath.Join(prebuilt, executableArtifactName(binary.name, "1.0.0", target)), ".")
	}
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	input := buildInput{output: t.TempDir(), kadoPrebuilt: prebuilt, a2aPrebuilt: prebuilt, mcpPrebuilt: mcp, mcpCommit: c.Commit, mcpDigests: map[string]string{target.goos + "/" + target.goarch: digest}, source: releaseIdentity{Version: "1.0.0", Repository: releaseRepository}, builtAt: time.Unix(1700000000, 0).UTC(), privateKey: key, publicKey: key.Public().(ed25519.PublicKey), a2aLicense: []byte("Apache-2.0"), a2a: a2aPreparedSource{Lock: a2aSourceLock{Version: "0.1.0", Repository: a2aRepository, License: a2aLicenseLock{SPDX: "Apache-2.0"}}}}
	files := map[string]builtFile{}
	add := func(name string, b []byte, mode fs.FileMode) (releaseclient.File, error) {
		f := releaseclient.File{Name: name, URL: "https://kado.so/install/releases/1.0.0/" + name, SHA256: payload.Digest(b), Size: int64(len(b))}
		files[name] = builtFile{file: f, data: b}
		return f, nil
	}
	register := func(name string, b []byte, f releaseclient.File) error {
		files[name] = builtFile{file: f, data: b}
		return nil
	}
	built, err := buildTargetArtifacts(input, target, "https://kado.so/install/releases/1.0.0", []byte("MIT"), []byte("guide"), add, register)
	if err != nil {
		t.Fatal(err)
	}
	if built.Payload == nil || built.NodeArchiveSHA256 != c.NodeArchiveSHA256 {
		t.Fatal("missing target payload identity")
	}
	b, err := releaseclient.VerifyCompleteTarget(built, files[built.Archive.Name].data, input.publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest.MCP != c || b.Manifest.Entries["maintenance"] == "" {
		t.Fatal("component lost in release")
	}
	sbom := string(files[built.SBOM.Name].data)
	for _, want := range []string{"SPDXRef-MCP", "SPDXRef-Node", "example", "mcp/app/dist/native/kado-mcp-host", "mcp/component.gen.json"} {
		if !strings.Contains(sbom, want) {
			t.Fatalf("SBOM missing %s", want)
		}
	}
	provenance, err := makeProvenance(input, files, []releaseclient.Target{built})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(provenance), c.Commit) || !strings.Contains(string(provenance), digest) || !strings.Contains(string(provenance), built.Payload.SHA256) {
		t.Fatal("provenance missing MCP inputs")
	}
}
