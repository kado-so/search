// Command mcp-package-probe is a G2 qualification harness, not the public launcher.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type payloadFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	Schema      int               `json:"schema"`
	Purpose     string            `json:"purpose"`
	Target      string            `json:"target"`
	NodeVersion string            `json:"nodeVersion"`
	Runtime     string            `json:"runtime"`
	Entries     map[string]string `json:"entries"`
	Packages    map[string]string `json:"packages"`
	Files       []payloadFile     `json:"files"`
}

func validPath(path string) bool {
	if path == "" || strings.ContainsAny(path, "\\:\x00") || strings.HasPrefix(path, "/") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, " .") != part {
			return false
		}
		base := strings.ToUpper(strings.Split(part, ".")[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return false
		}
	}
	return true
}

func verify(root, digest string) (manifest, error) {
	var m manifest
	expectedDigest, err := hex.DecodeString(digest)
	if err != nil || len(expectedDigest) != sha256.Size {
		return m, errors.New("invalid trusted manifest digest")
	}
	// Reject linked parents as well as links/reparse points in the payload.
	for parent := root; ; parent = filepath.Dir(parent) {
		if err := checkPlainPath(parent); err != nil {
			return m, err
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	if err := checkPlainPath(filepath.Join(root, "payload.gen.json")); err != nil {
		return m, err
	}
	bytes, err := os.ReadFile(filepath.Join(root, "payload.gen.json"))
	if err != nil {
		return m, err
	}
	actual := sha256.Sum256(bytes)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), digest) {
		return m, errors.New("manifest digest mismatch")
	}
	decoder := json.NewDecoder(strings.NewReader(string(bytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return m, errors.New("trailing manifest content")
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	if m.Schema != 1 || m.Purpose != "g2-qualification-only" || m.Target != platform+"-"+arch {
		return m, errors.New("unsupported payload schema or target")
	}
	node := "runtime/node"
	if runtime.GOOS == "windows" {
		node += ".exe"
	}
	if m.Runtime != node {
		return m, errors.New("unexpected runtime entrypoint")
	}
	roles := map[string]string{"cli": "app/dist/cli/index.js", "bridge": "app/dist/bridge/index.js", "probe": "app/qualification/probe.mjs", "fixture": "app/qualification/fixture.mjs"}
	if len(m.Entries) != len(roles) {
		return m, errors.New("unexpected executable roles")
	}
	for role, path := range roles {
		if m.Entries[role] != path {
			return m, errors.New("unexpected application entrypoint")
		}
	}
	wanted := map[string]payloadFile{}
	folded := map[string]bool{}
	directories := map[string]bool{".": true}
	for _, file := range m.Files {
		if !validPath(file.Path) || file.Size < 0 || file.Path == "payload.gen.json" || folded[strings.ToLower(file.Path)] {
			return m, errors.New("invalid or duplicate payload path")
		}
		if b, e := hex.DecodeString(file.SHA256); e != nil || len(b) != sha256.Size {
			return m, errors.New("invalid payload hash")
		}
		folded[strings.ToLower(file.Path)] = true
		wanted[file.Path] = file
		for d := filepath.Dir(filepath.FromSlash(file.Path)); d != "."; d = filepath.Dir(d) {
			directories[filepath.ToSlash(d)] = true
		}
	}
	host := "app/dist/native/kado-mcp-host"
	if runtime.GOOS == "windows" {
		host += ".exe"
	}
	for _, path := range append([]string{m.Runtime, host}, m.Entries["cli"], m.Entries["bridge"], m.Entries["probe"], m.Entries["fixture"]) {
		if _, ok := wanted[path]; !ok {
			return m, errors.New("missing entrypoint")
		}
	}
	seen := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := checkPlainPath(path); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if !directories[rel] {
				return fmt.Errorf("unexpected directory: %s", rel)
			}
			return nil
		}
		if rel == "payload.gen.json" {
			return nil
		}
		file, ok := wanted[rel]
		if !ok {
			return fmt.Errorf("unexpected file: %s", rel)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != file.Size {
			return fmt.Errorf("payload size/type mismatch: %s", rel)
		}
		handle, err := os.Open(path)
		if err != nil {
			return err
		}
		sum := sha256.New()
		_, err = io.Copy(sum, handle)
		closeErr := handle.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if !strings.EqualFold(hex.EncodeToString(sum.Sum(nil)), file.SHA256) {
			return fmt.Errorf("payload hash mismatch: %s", rel)
		}
		seen++
		return nil
	})
	if err == nil && seen != len(wanted) {
		err = errors.New("incomplete payload")
	}
	return m, err
}

func cleanEnvironment(values []string) []string {
	result := []string{}
	for _, value := range values {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if strings.HasPrefix(key, "NODE_") || strings.HasPrefix(key, "NAPI_") || strings.HasPrefix(key, "BUN_") || strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") || strings.HasPrefix(key, "NPM_CONFIG_") || key == "OPENSSL_CONF" || key == "OPENSSL_MODULES" {
			continue
		}
		result = append(result, value)
	}
	return result
}

func main() {
	root := flag.String("bundle", "", "absolute extracted payload path")
	digest := flag.String("digest", "", "trusted manifest SHA-256 from the builder, not from this payload")
	role := flag.String("entry", "cli", "cli, bridge, probe, fixture, or verify")
	lifetime := flag.String("lifetime", "persistent", "foreground containment or persistent session ownership")
	flag.Parse()
	if !filepath.IsAbs(*root) {
		fmt.Fprintln(os.Stderr, "bundle path must be absolute")
		os.Exit(1)
	}
	m, err := verify(filepath.Clean(*root), *digest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *role == "verify" {
		fmt.Println("payload verified")
		return
	}
	entry, ok := m.Entries[*role]
	if !ok {
		fmt.Fprintln(os.Stderr, "unknown entrypoint")
		os.Exit(1)
	}
	if *lifetime != "foreground" && *lifetime != "persistent" {
		fmt.Fprintln(os.Stderr, "invalid lifetime")
		os.Exit(1)
	}
	if err := prepareLifetime(*lifetime); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	args := append([]string{"--no-global-search-paths", filepath.Join(*root, filepath.FromSlash(entry))}, flag.Args()...)
	command := exec.Command(filepath.Join(*root, filepath.FromSlash(m.Runtime)), args...)
	command.Env = cleanEnvironment(os.Environ())
	command.Dir = *root
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := runContained(command, *lifetime); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
