package skillclient

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kado-so/search/internal/releaseclient"
	kadoskill "github.com/kado-so/search/skills/kado-search"
)

var (
	ErrInvalidRelease    = errors.New("skill release is invalid")
	ErrUnsupportedCLI    = errors.New("skill release requires a newer Kado CLI")
	ErrUnsupportedAgent  = errors.New("agent does not have a supported skill location")
	ErrExternallyManaged = errors.New("skill installation is not managed by Kado")
	ErrLocallyModified   = errors.New("skill installation was locally modified")
)

type Manager struct {
	ConfigDir      string
	HomeDir        string
	BaseURL        string
	PublicKey      string
	CurrentVersion string
	Fetcher        releaseclient.Fetcher
}

type InstallOptions struct {
	CurrentAgent string
	Agents       []string
	All          bool
}

type InstallResult struct {
	Version      string
	Installed    []Installation
	OtherAgents  []string
	UsedFallback bool
}

type UpdateResult struct {
	Version  string
	Updated  []Installation
	Current  []Installation
	Failures map[string]string
}

type Status struct {
	Installations []Installation    `json:"installations"`
	Failures      map[string]string `json:"failures,omitempty"`
}

func (manager Manager) Install(
	ctx context.Context,
	options InstallOptions,
) (InstallResult, error) {
	files, version, fallback, err := manager.latest(ctx, true)
	if err != nil {
		return InstallResult{}, err
	}
	agents := append([]string(nil), options.Agents...)
	defaultAll := len(agents) == 0
	if defaultAll && options.CurrentAgent != "" && options.CurrentAgent != "default" {
		agents = append(agents, options.CurrentAgent)
	}
	agents = expandSkillAgents(agents)
	others := DetectInstalledAgents(manager.HomeDir, options.CurrentAgent)
	if options.All || defaultAll {
		agents = append(agents, expandSkillAgents(others)...)
		agents = append(agents, "agents")
	}
	agents = canonicalSkillAgents(agents)
	if len(agents) == 0 {
		return InstallResult{}, ErrUnsupportedAgent
	}
	registry, err := readRegistry(manager.ConfigDir)
	if err != nil {
		return InstallResult{}, err
	}
	result := InstallResult{
		Version:      version,
		OtherAgents:  others,
		UsedFallback: fallback,
	}
	for _, agent := range agents {
		destination, err := Destination(manager.HomeDir, agent)
		if err != nil {
			return result, err
		}
		item, err := installFiles(destination, agent, "user", version, files)
		if err != nil {
			return result, fmt.Errorf("%s: %w", agent, err)
		}
		registry.Installations = upsertInstallation(registry.Installations, item)
		result.Installed = append(result.Installed, item)
	}
	if err := writeRegistry(manager.ConfigDir, registry); err != nil {
		return result, err
	}
	return result, nil
}

func (manager Manager) Update(ctx context.Context) (UpdateResult, error) {
	registry, err := readRegistry(manager.ConfigDir)
	if err != nil {
		return UpdateResult{}, err
	}
	registry, discoveryFailures := manager.reconcileRegistry(registry)
	files, version, _, err := manager.latest(ctx, manager.PublicKey == "")
	if err != nil {
		return UpdateResult{}, err
	}
	result := UpdateResult{
		Version:  version,
		Failures: discoveryFailures,
	}
	if len(registry.Installations) == 0 {
		return result, writeRegistry(manager.ConfigDir, registry)
	}
	desiredDigest := kadoskill.Digest(files)
	for index, item := range registry.Installations {
		expected, destinationErr := Destination(manager.HomeDir, item.Agent)
		if destinationErr != nil || expected != item.Path || item.Scope != "user" {
			result.Failures[item.Path] = "invalid_destination"
			continue
		}
		if _, unsafe := result.Failures[item.Path]; unsafe {
			continue
		}
		if item.Version == version && item.ContentSHA256 == desiredDigest {
			if err := verifyManagedInstallation(item); err == nil {
				result.Current = append(result.Current, item)
				continue
			}
		}
		updated, err := installFiles(
			item.Path,
			item.Agent,
			item.Scope,
			version,
			files,
		)
		if err != nil {
			result.Failures[item.Path] = publicFailure(err)
			continue
		}
		registry.Installations[index] = updated
		result.Updated = append(result.Updated, updated)
	}
	if err := writeRegistry(manager.ConfigDir, registry); err != nil {
		return result, err
	}
	return result, nil
}

func (manager Manager) Status() (Status, error) {
	registry, err := readRegistry(manager.ConfigDir)
	if err != nil {
		return Status{}, err
	}
	registry, failures := manager.reconcileRegistry(registry)
	if err := writeRegistry(manager.ConfigDir, registry); err != nil {
		return Status{}, err
	}
	verified := make([]Installation, 0, len(registry.Installations))
	for _, item := range registry.Installations {
		if _, failed := failures[item.Path]; !failed {
			verified = append(verified, item)
		}
	}
	return Status{Installations: verified, Failures: failures}, nil
}

func (manager Manager) Uninstall(agents []string, all bool) ([]Installation, error) {
	registry, err := readRegistry(manager.ConfigDir)
	if err != nil {
		return nil, err
	}
	registry, _ = manager.reconcileRegistry(registry)
	selected := make(map[string]struct{})
	for _, agent := range canonicalSkillAgents(expandSkillAgents(agents)) {
		selected[agent] = struct{}{}
	}
	var removed, retained []Installation
	for _, item := range registry.Installations {
		_, requested := selected[item.Agent]
		if !all && !requested {
			retained = append(retained, item)
			continue
		}
		expected, destinationErr := Destination(manager.HomeDir, item.Agent)
		if destinationErr != nil || expected != item.Path || item.Scope != "user" {
			return removed, ErrExternallyManaged
		}
		if err := verifyManagedInstallation(item); err != nil {
			return removed, err
		}
		if err := os.RemoveAll(item.Path); err != nil {
			return removed, err
		}
		removed = append(removed, item)
	}
	registry.Installations = retained
	if err := writeRegistry(manager.ConfigDir, registry); err != nil {
		return removed, err
	}
	return removed, nil
}

func (manager Manager) latest(
	ctx context.Context,
	allowFallback bool,
) (map[string][]byte, string, bool, error) {
	if manager.PublicKey != "" && manager.BaseURL != "" {
		files, version, err := manager.fetchLatest(ctx)
		if err == nil {
			return files, version, false, nil
		}
		if !allowFallback {
			return nil, "", false, err
		}
	}
	files, err := kadoskill.Bundle()
	if err != nil {
		return nil, "", true, err
	}
	version := kadoskill.Version()
	if version == "" {
		return nil, "", true, ErrInvalidRelease
	}
	return files, version, true, nil
}

func (manager Manager) fetchLatest(
	ctx context.Context,
) (map[string][]byte, string, error) {
	base := strings.TrimSuffix(manager.BaseURL, "/")
	metadataURL := base + "/install/skills/kado-search/latest/metadata.json"
	fetcher := manager.Fetcher
	if fetcher == nil {
		fetcher = releaseclient.HTTPFetcher{}
	}
	encoded, err := fetcher.Fetch(ctx, metadataURL, MaxMetadataSize)
	if err != nil {
		return nil, "", err
	}
	signature, err := fetcher.Fetch(ctx, metadataURL+".sig", 64)
	if err != nil {
		return nil, "", err
	}
	metadata, err := VerifyMetadata(encoded, signature, manager.PublicKey, metadataURL)
	if err != nil {
		return nil, "", ErrInvalidRelease
	}
	if lessVersion(manager.CurrentVersion, metadata.MinimumCLIVersion) {
		return nil, "", ErrUnsupportedCLI
	}
	archive, err := fetcher.Fetch(ctx, metadata.Archive.URL, MaxArchiveSize)
	if err != nil || int64(len(archive)) != metadata.Archive.Size ||
		releaseclient.Digest(archive) != metadata.Archive.SHA256 {
		return nil, "", ErrInvalidRelease
	}
	files, err := ExtractArchive(archive)
	if err != nil {
		return nil, "", err
	}
	return files, metadata.Version, nil
}

func installFiles(
	destination, agent, scope, version string,
	files map[string][]byte,
) (Installation, error) {
	if !filepath.IsAbs(destination) {
		return Installation{}, errors.New("skill destination is invalid")
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Installation{}, err
	}
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() ||
		info.Mode()&os.ModeSymlink != 0 {
		return Installation{}, errors.New("skill destination parent is unsafe")
	}
	if _, err := os.Lstat(destination); err == nil {
		existing := Installation{Path: destination}
		receipt, receiptErr := readReceipt(destination)
		if receiptErr != nil {
			return Installation{}, ErrExternallyManaged
		}
		existing.Agent = receipt.Agent
		existing.Scope = receipt.Scope
		existing.Version = receipt.Version
		existing.ContentSHA256 = receipt.ContentSHA256
		if err := verifyManagedInstallation(existing); err != nil {
			return Installation{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Installation{}, err
	}
	staging, err := os.MkdirTemp(parent, ".kado-skill-*")
	if err != nil {
		return Installation{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staging)
		}
	}()
	for _, name := range sortedFileNames(files) {
		target := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return Installation{}, err
		}
		if err := os.WriteFile(target, files[name], 0o644); err != nil {
			return Installation{}, err
		}
	}
	item := Installation{
		Agent:         agent,
		Scope:         scope,
		Path:          destination,
		Version:       version,
		ContentSHA256: kadoskill.Digest(files),
	}
	if err := writeJSONAtomic(
		filepath.Join(staging, receiptName),
		newReceipt(item),
		0o600,
	); err != nil {
		return Installation{}, err
	}
	backup, err := os.MkdirTemp(parent, ".kado-skill-backup-*")
	if err != nil {
		return Installation{}, err
	}
	if err := os.Remove(backup); err != nil {
		return Installation{}, err
	}
	backupNeedsCleanup := false
	defer func() {
		if backupNeedsCleanup {
			_ = os.RemoveAll(backup)
		}
	}()
	hadExisting := false
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Rename(destination, backup); err != nil {
			return Installation{}, err
		}
		hadExisting = true
		backupNeedsCleanup = true
	}
	if err := os.Rename(staging, destination); err != nil {
		if hadExisting {
			if rollbackErr := os.Rename(backup, destination); rollbackErr != nil {
				backupNeedsCleanup = false
				return Installation{}, errors.Join(err, rollbackErr)
			}
		}
		return Installation{}, err
	}
	keep = true
	if hadExisting {
		_ = os.RemoveAll(backup)
		backupNeedsCleanup = false
	}
	return item, nil
}

func verifyManagedInstallation(item Installation) error {
	info, err := os.Lstat(item.Path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrExternallyManaged
	}
	receipt, err := readReceipt(item.Path)
	if err != nil || receipt.Agent != item.Agent || receipt.Scope != item.Scope ||
		receipt.Version != item.Version ||
		receipt.ContentSHA256 != item.ContentSHA256 {
		return ErrExternallyManaged
	}
	files := make(map[string][]byte)
	err = filepath.WalkDir(item.Path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(item.Path, path)
		if err != nil {
			return err
		}
		if filepath.ToSlash(relative) == receiptName {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			info.Size() > maxSkillFile {
			return ErrLocallyModified
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = value
		return nil
	})
	if err != nil || kadoskill.Digest(files) != item.ContentSHA256 {
		return ErrLocallyModified
	}
	return nil
}

// reconcileRegistry scans every supported destination and merges verified
// Kado-owned installations into the registry. Existing registry entries remain
// the trust anchor when disk metadata disagrees, while an unregistered receipt
// is adopted only after its agent, destination, and content digest all verify.
func (manager Manager) reconcileRegistry(value registry) (registry, map[string]string) {
	failures := make(map[string]string)
	registered := make(map[string]Installation, len(value.Installations))
	for _, item := range value.Installations {
		registered[item.Path] = item
	}

	destinations := KnownDestinations(manager.HomeDir)
	agents := make([]string, 0, len(destinations))
	for agent := range destinations {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	seen := make(map[string]struct{}, len(destinations))
	for _, agent := range agents {
		destination := destinations[agent]
		seen[destination] = struct{}{}
		tracked, wasRegistered := registered[destination]
		info, err := os.Lstat(destination)
		if errors.Is(err, os.ErrNotExist) {
			if wasRegistered {
				failures[destination] = "externally_managed"
			}
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			failures[destination] = "externally_managed"
			continue
		}
		receipt, err := readReceipt(destination)
		if err != nil || receipt.Agent != agent || receipt.Agent != canonicalSkillAgent(receipt.Agent) ||
			receipt.Scope != "user" {
			failures[destination] = "externally_managed"
			continue
		}
		discovered := Installation{
			Agent:         receipt.Agent,
			Scope:         receipt.Scope,
			Path:          destination,
			Version:       receipt.Version,
			ContentSHA256: receipt.ContentSHA256,
		}
		if err := verifyManagedInstallation(discovered); err != nil {
			failures[destination] = publicFailure(err)
			continue
		}
		if wasRegistered && tracked != discovered {
			failures[destination] = "externally_managed"
			continue
		}
		value.Installations = upsertInstallation(value.Installations, discovered)
	}
	for _, item := range value.Installations {
		if _, known := seen[item.Path]; !known {
			failures[item.Path] = "invalid_destination"
		}
	}
	return value, failures
}

func upsertInstallation(values []Installation, item Installation) []Installation {
	for index := range values {
		if values[index].Path == item.Path {
			values[index] = item
			return values
		}
	}
	return append(values, item)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{})
	output := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		output = append(output, value)
	}
	return output
}

func expandSkillAgents(values []string) []string {
	output := append([]string(nil), values...)
	for _, value := range values {
		switch value {
		case "gemini-cli":
			output = append(output, "antigravity")
		case "antigravity":
			output = append(output, "gemini-cli")
		}
	}
	return uniqueStrings(output)
}

func canonicalSkillAgents(values []string) []string {
	output := make([]string, 0, len(values))
	for _, value := range values {
		output = append(output, canonicalSkillAgent(value))
	}
	return uniqueStrings(output)
}

func lessVersion(left, right string) bool {
	parse := func(value string) [3]uint64 {
		var output [3]uint64
		core := strings.SplitN(value, "-", 2)[0]
		for index, part := range strings.Split(core, ".") {
			if index >= len(output) {
				break
			}
			output[index], _ = strconv.ParseUint(part, 10, 64)
		}
		return output
	}
	l, r := parse(left), parse(right)
	for index := range l {
		if l[index] != r[index] {
			return l[index] < r[index]
		}
	}
	return false
}

func publicFailure(err error) string {
	switch {
	case errors.Is(err, ErrLocallyModified):
		return "locally_modified"
	case errors.Is(err, ErrExternallyManaged):
		return "externally_managed"
	default:
		return "update_failed"
	}
}
