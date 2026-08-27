// Package testfixture exposes authoritative Search documents to tests without
// embedding them in the production CLI.
package testfixture

import (
	"embed"
	"errors"
	"io/fs"
	"strings"
)

//go:embed v1/*.json v2/*.json
var fixtures embed.FS

type Version string

const (
	V1 Version = "v1"
	V2 Version = "v2"
)

func Load(name string) ([]byte, error) {
	return LoadVersion(V1, name)
}

func LoadVersion(version Version, name string) ([]byte, error) {
	if version != V1 && version != V2 {
		return nil, errors.New("invalid Search fixture version")
	}
	if name == "" || strings.ContainsAny(name, "/\\.") {
		return nil, errors.New("invalid Search fixture name")
	}
	return fixtures.ReadFile(string(version) + "/" + name + ".json")
}

func Names() ([]string, error) {
	return NamesVersion(V1)
}

func NamesVersion(version Version) ([]string, error) {
	if version != V1 && version != V2 {
		return nil, errors.New("invalid Search fixture version")
	}
	entries, err := fs.ReadDir(fixtures, string(version))
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
