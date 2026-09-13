package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kado-so/search/internal/payload"
)

// readMCPComponent consumes the exact committed component artifact produced on
// its native qualification runner. The release job supplies an independently
// pinned commit and manifest digest; a payload cannot nominate its own trust.
func readMCPComponent(directory, commit, expectedDigest string, target buildTarget) (payload.Component, map[string][]byte, error) {
	var component payload.Component
	if !commitPattern.MatchString(commit) || !payload.ValidDigest(expectedDigest) {
		return component, nil, errors.New("pinned MCP component commit and digest are required")
	}
	encoded, err := payload.ReadFile(directory, "payload.gen.json", payload.MaxManifest)
	if err != nil || payload.Digest(encoded) != expectedDigest {
		return component, nil, errors.New("MCP component manifest checksum mismatch")
	}
	var manifest struct {
		Schema      int               `json:"schema"`
		Purpose     string            `json:"purpose"`
		Target      string            `json:"target"`
		NodeVersion string            `json:"nodeVersion"`
		Runtime     string            `json:"runtime"`
		Entries     map[string]string `json:"entries"`
		Packages    map[string]string `json:"packages"`
		Component   payload.Component `json:"component"`
		Files       []struct {
			Path   string `json:"path"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	d := json.NewDecoder(strings.NewReader(string(encoded)))
	d.DisallowUnknownFields()
	if err := d.Decode(&manifest); err != nil {
		return component, nil, err
	}
	if d.Decode(new(any)) != io.EOF {
		return component, nil, payload.ErrInvalid
	}
	if !payload.ValidVersion(manifest.Component.Version) || !payload.ValidVersion(manifest.Component.NodeVersion) || !payload.ValidDigest(manifest.Component.LockSHA256) || !payload.ValidDigest(manifest.Component.NodeArchiveSHA256) {
		return component, nil, payload.ErrInvalid
	}
	platform := target.goos
	if platform == "windows" {
		platform = "win32"
	}
	arch := target.goarch
	if arch == "amd64" {
		arch = "x64"
	}
	if manifest.Schema != 1 || manifest.Purpose != "kado-release-component-v1" || manifest.Target != platform+"-"+arch || manifest.Component.Commit != commit || manifest.NodeVersion != manifest.Component.NodeVersion || len(manifest.Files) > payload.MaxFiles {
		return component, nil, payload.ErrInvalid
	}
	expectedEntries := map[string]string{"cli": "app/dist/cli/index.js", "bridge": "app/dist/bridge/index.js", "maintenance": "app/dist/maintenance/index.js"}
	if len(manifest.Entries) != len(expectedEntries) {
		return component, nil, payload.ErrInvalid
	}
	for role, p := range expectedEntries {
		if manifest.Entries[role] != p {
			return component, nil, payload.ErrInvalid
		}
	}
	wantRuntime := "runtime/node"
	if target.goos == "windows" {
		wantRuntime += ".exe"
	}
	if manifest.Runtime != wantRuntime {
		return component, nil, payload.ErrInvalid
	}
	wanted := map[string]bool{"payload.gen.json": true}
	files := map[string][]byte{}
	var total int64
	for _, f := range manifest.Files {
		if !payload.ValidPath(f.Path) || f.Size < 0 || f.Size > payload.MaxFile || wanted[f.Path] {
			return component, nil, payload.ErrInvalid
		}
		total += f.Size
		if total > payload.MaxExpanded {
			return component, nil, payload.ErrInvalid
		}
		value, err := payload.ReadFile(directory, f.Path, f.Size)
		if err != nil || int64(len(value)) != f.Size || payload.Digest(value) != f.SHA256 {
			return component, nil, payload.ErrInvalid
		}
		wanted[f.Path] = true
		files["mcp/"+f.Path] = value
	}
	err = filepath.WalkDir(directory, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := payload.PlainPath(p); err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(directory, p)
		if err != nil {
			return err
		}
		if !wanted[filepath.ToSlash(rel)] {
			return payload.ErrInvalid
		}
		return nil
	})
	if err != nil {
		return component, nil, err
	}
	// Preserve materialized package inventory and frozen input identity inside
	// the final signed tree, alongside per-file hashes in the outer manifest.
	files["mcp/component.gen.json"] = encoded
	return manifest.Component, files, nil
}

func readComponentDigests(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 16384 {
		return nil, errors.New("MCP component digest index is unavailable")
	}
	var values map[string]string
	if json.Unmarshal(b, &values) != nil || len(values) != len(releaseTargets) {
		return nil, errors.New("MCP digest index must contain all six targets")
	}
	for _, t := range releaseTargets {
		if !payload.ValidDigest(values[t.goos+"/"+t.goarch]) {
			return nil, payload.ErrInvalid
		}
	}
	return values, nil
}
