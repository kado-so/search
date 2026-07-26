// Package testfixture exposes authoritative Search documents to tests without
// embedding them in the production CLI.
package testfixture

import (
	"embed"
	"errors"
	"io/fs"
	"strings"
)

//go:embed *.json
var fixtures embed.FS

func Load(name string) ([]byte, error) {
	if name == "" || strings.ContainsAny(name, "/\\.") {
		return nil, errors.New("invalid Search fixture name")
	}
	return fixtures.ReadFile(name + ".json")
}

func Names() ([]string, error) {
	entries, err := fs.ReadDir(fixtures, ".")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	return names, nil
}
