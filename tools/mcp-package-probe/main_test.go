package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testPayload(t *testing.T) (string, manifest) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	platform, arch := runtime.GOOS, runtime.GOARCH
	if platform == "windows" {
		platform = "win32"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	node := "runtime/node"
	if runtime.GOOS == "windows" {
		node += ".exe"
	}
	m := manifest{Schema: 1, Purpose: "g2-qualification-only", Target: platform + "-" + arch, Runtime: node, Entries: map[string]string{"cli": "app/dist/cli/index.js", "bridge": "app/dist/bridge/index.js", "probe": "app/qualification/probe.mjs", "fixture": "app/qualification/fixture.mjs"}}
	paths := []string{node}
	for _, path := range m.Entries {
		paths = append(paths, path)
	}
	for _, path := range paths {
		p := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		bytes := []byte(path)
		if err := os.WriteFile(p, bytes, 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(bytes)
		m.Files = append(m.Files, payloadFile{path, int64(len(bytes)), hex.EncodeToString(hash[:])})
	}
	return root, m
}
func writeManifest(t *testing.T, root string, m manifest) string {
	t.Helper()
	bytes, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "payload.gen.json"), bytes, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:])
}
func TestVerifiedPayloadRejectsMutation(t *testing.T) {
	root, m := testPayload(t)
	digest := writeManifest(t, root, m)
	if _, err := verify(root, digest); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, filepath.FromSlash(m.Files[0].Path))
	if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(root, digest); err == nil {
		t.Fatal("accepted changed runtime")
	}
}
func TestRejectManifestAndInventoryAttacks(t *testing.T) {
	for _, name := range []string{"traversal", "case-collision", "extra-role", "missing", "extra-file", "wrong-target", "untrusted-manifest"} {
		t.Run(name, func(t *testing.T) {
			root, m := testPayload(t)
			switch name {
			case "traversal":
				m.Files[0].Path = "../escape"
			case "case-collision":
				file := m.Files[0]
				file.Path = strings.ToUpper(file.Path)
				m.Files = append(m.Files, file)
			case "extra-role":
				m.Entries["arbitrary"] = "app/arbitrary.js"
			case "missing":
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(m.Files[0].Path))); err != nil {
					t.Fatal(err)
				}
			case "extra-file":
				if err := os.WriteFile(filepath.Join(root, "extra.node"), []byte("unexpected"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong-target":
				m.Target = "other-target"
			}
			digest := writeManifest(t, root, m)
			if name == "untrusted-manifest" {
				digest = strings.Repeat("0", 64)
			}
			if _, err := verify(root, digest); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}
func TestPortablePaths(t *testing.T) {
	for _, path := range []string{"/absolute", "../escape", "x/../escape", "x\\escape", "x:y", "a//b", "a/CON.txt", "a/trailing.", "a/trailing ", "a/./b"} {
		if validPath(path) {
			t.Fatalf("accepted %q", path)
		}
	}
	if !validPath("app/ü & spaces/file.js") {
		t.Fatal("rejected portable Unicode path")
	}
}
func TestEnvironmentRemovesRuntimeInjection(t *testing.T) {
	env := cleanEnvironment([]string{"NODE_OPTIONS=evil", "node_path=evil", "NAPI_RS_NATIVE_LIBRARY_PATH=evil", "BUN_OPTIONS=evil", "LD_PRELOAD=evil", "DYLD_INSERT_LIBRARIES=evil", "OPENSSL_CONF=evil", "npm_config_node_options=evil", "KADO_MCP_HOME_DIR=isolated", "PATH=system"})
	if strings.Join(env, "|") != "KADO_MCP_HOME_DIR=isolated|PATH=system" {
		t.Fatal(env)
	}
}
