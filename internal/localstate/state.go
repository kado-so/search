// Package localstate owns non-secret host and identity state next to config.
package localstate

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	hostFileName       = "host.json"
	identitiesFileName = "identities.json"
	stateVersion       = 1
)

var stateMu sync.Mutex

type Host struct {
	Version int    `json:"version"`
	ID      string `json:"host_id"`
}

type identityRegistry struct {
	Version    int      `json:"version"`
	Identities []string `json:"identities"`
}

func EnsureHost(configDir string) (Host, error) {
	stateMu.Lock()
	defer stateMu.Unlock()
	if err := ensureConfigDirectory(configDir); err != nil {
		return Host{}, err
	}
	path := filepath.Join(configDir, hostFileName)
	host, err := readHost(path)
	if err == nil {
		return host, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Host{}, err
	}
	random := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		return Host{}, err
	}
	host = Host{
		Version: stateVersion,
		ID:      "host_" + base64.RawURLEncoding.EncodeToString(random),
	}
	clear(random)
	if err := writeExclusive(path, host); errors.Is(err, os.ErrExist) {
		return readHost(path)
	} else if err != nil {
		return Host{}, err
	}
	return host, nil
}

func ListIdentities(configDir string) ([]string, error) {
	stateMu.Lock()
	defer stateMu.Unlock()
	registry, err := readRegistry(filepath.Join(configDir, identitiesFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return append([]string(nil), registry.Identities...), nil
}

func AddIdentity(configDir, agent string) error {
	return updateIdentities(configDir, agent, true)
}

func RemoveIdentity(configDir, agent string) error {
	return updateIdentities(configDir, agent, false)
}

func updateIdentities(configDir, agent string, add bool) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	if err := ensureConfigDirectory(configDir); err != nil {
		return err
	}
	path := filepath.Join(configDir, identitiesFileName)
	registry, err := readRegistry(path)
	if errors.Is(err, os.ErrNotExist) {
		registry = identityRegistry{Version: stateVersion}
	} else if err != nil {
		return err
	}
	values := make(map[string]struct{}, len(registry.Identities)+1)
	for _, identity := range registry.Identities {
		values[identity] = struct{}{}
	}
	if add {
		values[agent] = struct{}{}
	} else {
		delete(values, agent)
	}
	registry.Identities = registry.Identities[:0]
	for identity := range values {
		registry.Identities = append(registry.Identities, identity)
	}
	sort.Strings(registry.Identities)
	return writeAtomic(path, registry)
}

func ensureConfigDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("invalid configuration directory")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("unsafe configuration directory")
	}
	return os.Chmod(path, 0o700)
}

func readHost(path string) (Host, error) {
	var host Host
	if err := readExact(path, &host); err != nil {
		return Host{}, err
	}
	if host.Version != stateVersion ||
		!strings.HasPrefix(host.ID, "host_") ||
		len(host.ID) != len("host_")+22 {
		return Host{}, errors.New("invalid host identity")
	}
	return host, nil
}

func readRegistry(path string) (identityRegistry, error) {
	var registry identityRegistry
	if err := readExact(path, &registry); err != nil {
		return identityRegistry{}, err
	}
	if registry.Version != stateVersion || !sort.StringsAreSorted(registry.Identities) {
		return identityRegistry{}, errors.New("invalid identity registry")
	}
	for index, identity := range registry.Identities {
		if identity == "" || index > 0 && registry.Identities[index-1] == identity {
			return identityRegistry{}, errors.New("invalid identity registry")
		}
	}
	return registry, nil
}

func readExact(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid local state")
	}
	return nil
}

func writeExclusive(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	keep := true
	defer func() {
		_ = file.Close()
		if keep {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	keep = false
	return nil
}

func writeAtomic(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kado-state-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(name)
	}()
	if err := temporary.Chmod(0o600); err != nil {
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
	return replaceStateFile(name, path)
}
