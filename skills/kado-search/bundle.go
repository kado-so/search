// Package kado_search exposes the release-time Search skill bundled with Kado.
package kado_search

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"regexp"
	"sort"
)

// Files is the offline fallback used when the authoritative signed skill
// release cannot be reached.
//
//go:embed SKILL.md assets/* agents/*
var Files embed.FS

// MinimumCLIVersion is the oldest CLI version compatible with this skill
// release. It changes only when the skill starts relying on newer CLI
// behavior, not whenever the CLI itself is released.
const MinimumCLIVersion = "0.1.5"

// Bundle returns the embedded regular files in stable path order.
func Bundle() (map[string][]byte, error) {
	output := make(map[string][]byte)
	err := fs.WalkDir(Files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		value, err := Files.ReadFile(path)
		if err != nil {
			return err
		}
		output[path] = value
		return nil
	})
	return output, err
}

// Digest returns a stable digest over paths and bytes.
func Digest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	digest := sha256.New()
	for _, name := range names {
		_, _ = digest.Write([]byte(name))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(files[name])
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// Version returns the canonical version declared by SKILL.md.
func Version() string {
	value, err := Files.ReadFile("SKILL.md")
	if err != nil {
		return ""
	}
	match := regexp.MustCompile(`(?m)^  version: "([^"]+)"$`).FindSubmatch(value)
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}
