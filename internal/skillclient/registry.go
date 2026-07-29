package skillclient

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	registryVersion = 1
	registryName    = "skill-installs.json"
	receiptName     = ".kado-install.json"
)

type Installation struct {
	Agent         string `json:"agent"`
	Scope         string `json:"scope"`
	Path          string `json:"path"`
	Version       string `json:"version"`
	ContentSHA256 string `json:"content_sha256"`
}

type registry struct {
	Version       int            `json:"version"`
	Installations []Installation `json:"installations"`
}

type receipt struct {
	SchemaVersion int    `json:"schema_version"`
	Owner         string `json:"owner"`
	Agent         string `json:"agent"`
	Scope         string `json:"scope"`
	Version       string `json:"version"`
	ContentSHA256 string `json:"content_sha256"`
	InstalledAt   string `json:"installed_at"`
}

func readRegistry(configDir string) (registry, error) {
	path := filepath.Join(configDir, registryName)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return registry{Version: registryVersion}, nil
	}
	if err != nil {
		return registry{}, err
	}
	defer file.Close()
	var value registry
	decoder := json.NewDecoder(io.LimitReader(file, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return registry{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) ||
		value.Version != registryVersion {
		return registry{}, errors.New("skill installation registry is invalid")
	}
	for index, item := range value.Installations {
		if item.Agent == "" || item.Path == "" || !filepath.IsAbs(item.Path) ||
			index > 0 && value.Installations[index-1].Path >= item.Path {
			return registry{}, errors.New("skill installation registry is invalid")
		}
	}
	return value, nil
}

func writeRegistry(configDir string, value registry) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	sort.Slice(value.Installations, func(left, right int) bool {
		return value.Installations[left].Path < value.Installations[right].Path
	})
	return writeJSONAtomic(filepath.Join(configDir, registryName), value, 0o600)
}

func newReceipt(item Installation) receipt {
	return receipt{
		SchemaVersion: registryVersion,
		Owner:         "kado",
		Agent:         item.Agent,
		Scope:         item.Scope,
		Version:       item.Version,
		ContentSHA256: item.ContentSHA256,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
	}
}

func readReceipt(directory string) (receipt, error) {
	file, err := os.Open(filepath.Join(directory, receiptName))
	if err != nil {
		return receipt{}, err
	}
	defer file.Close()
	var value receipt
	decoder := json.NewDecoder(io.LimitReader(file, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return receipt{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) ||
		value.SchemaVersion != registryVersion || value.Owner != "kado" {
		return receipt{}, errors.New("skill installation receipt is invalid")
	}
	return value, nil
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kado-skill-state-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(name)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	keep = true
	return nil
}
