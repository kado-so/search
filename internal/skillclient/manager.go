package skillclient

import (
	"context"
	"errors"
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
	Removed      []Installation
	Failures     map[string]string
	UsedFallback bool
}

type UpdateResult struct {
	Version  string
	Updated  []Installation
	Current  []Installation
	Removed  []Installation
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
	for _, agent := range agents {
		registry.Targets = upsertTarget(registry.Targets, Target{Agent: agent, Scope: "user"})
	}
	sync, err := manager.sync(ctx, registry, true)
	return InstallResult{Version: embeddedVersion(SkillName), Installed: sync.Updated, Removed: sync.Removed, Failures: sync.Failures, OtherAgents: others, UsedFallback: manager.PublicKey == ""}, err
}

func (manager Manager) Update(ctx context.Context) (UpdateResult, error) {
	registry, err := readRegistry(manager.ConfigDir)
	if err != nil {
		return UpdateResult{}, err
	}
	return manager.sync(ctx, registry, manager.PublicKey == "")
}

func (manager Manager) sync(ctx context.Context, registry registry, allowFallback bool) (UpdateResult, error) {
	catalog, embedded, err := manager.desiredCatalog(ctx, allowFallback)
	if err != nil {
		return UpdateResult{}, err
	}
	staleCatalog := registry.CatalogRevision > catalog.Revision
	registry, discoveryFailures := manager.reconcileRegistry(registry, catalog)
	result := UpdateResult{Failures: discoveryFailures}
	desired := map[string]bool{}
	cache := map[string]EmbeddedRelease{}
	for _, target := range registry.Targets {
		for _, skill := range catalog.Skills {
			if skill.State != "active" {
				continue
			}
			variant, ok := SelectVariant(skill, target.Agent)
			if !ok {
				continue
			}
			key := skill.Name + ":" + variant.ID
			release, ok := cache[key]
			var resolutionErr error
			if !ok {
				release, resolutionErr = manager.resolveRelease(ctx, skill.Name, variant, embedded, allowFallback)
				if resolutionErr == nil {
					cache[key] = release
				}
			}
			destination, destinationErr := Destination(manager.HomeDir, target.Agent, skill.Name)
			desired[destination] = true
			if destinationErr != nil {
				result.Failures[key+":"+target.Agent] = "invalid_destination"
				continue
			}
			if resolutionErr != nil {
				result.Failures[destination] = publicFailure(resolutionErr)
				continue
			}
			existing, exists := installationAt(registry.Installations, destination)
			if staleCatalog && exists && verifyManagedInstallation(existing) == nil {
				delete(result.Failures, destination)
				result.Current = append(result.Current, existing)
				continue
			}
			digest := kadoskill.Digest(release.Files)
			if exists && existing.Variant == release.Metadata.Variant && lessVersion(release.Metadata.Version, existing.Version) && verifyManagedInstallation(existing) == nil {
				delete(result.Failures, destination)
				result.Current = append(result.Current, existing)
				continue
			}
			if exists && existing.Version == release.Metadata.Version && existing.Variant == release.Metadata.Variant && existing.ContentSHA256 == digest && verifyManagedInstallation(existing) == nil {
				delete(result.Failures, destination)
				result.Current = append(result.Current, existing)
				continue
			}
			item, installErr := installFiles(destination, target.Agent, target.Scope, release.Metadata.Version, release.Files, release.Metadata.Variant)
			if installErr != nil {
				result.Failures[destination] = publicFailure(installErr)
				continue
			}
			item.Name, item.Variant = skill.Name, release.Metadata.Variant
			delete(result.Failures, destination)
			registry.Installations = upsertInstallation(registry.Installations, item)
			result.Updated = append(result.Updated, item)
		}
	}
	var retained []Installation
	for _, item := range registry.Installations {
		if desired[item.Path] || staleCatalog {
			retained = append(retained, item)
			continue
		}
		if verifyManagedInstallation(item) != nil {
			result.Failures[item.Path] = "locally_modified"
			retained = append(retained, item)
			continue
		}
		if err := os.RemoveAll(item.Path); err != nil {
			result.Failures[item.Path] = publicFailure(err)
			retained = append(retained, item)
			continue
		}
		result.Removed = append(result.Removed, item)
	}
	registry.Installations = retained
	if !staleCatalog {
		registry.CatalogRevision = catalog.Revision
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
	catalog, _, catalogErr := EmbeddedCatalog()
	if catalogErr != nil {
		return Status{}, catalogErr
	}
	registry, failures := manager.reconcileRegistry(registry, catalog)
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
	catalog, _, _ := EmbeddedCatalog()
	registry, _ = manager.reconcileRegistry(registry, catalog)
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
		expected, destinationErr := Destination(manager.HomeDir, item.Agent, item.Name)
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
	if all {
		registry.Targets = nil
	} else {
		var targets []Target
		for _, target := range registry.Targets {
			if _, remove := selected[target.Agent]; !remove {
				targets = append(targets, target)
			}
		}
		registry.Targets = targets
	}
	if err := writeRegistry(manager.ConfigDir, registry); err != nil {
		return removed, err
	}
	return removed, nil
}

func (manager Manager) desiredCatalog(ctx context.Context, allowFallback bool) (Catalog, map[string]EmbeddedRelease, error) {
	embeddedCatalog, embedded, err := EmbeddedCatalog()
	if err != nil {
		return Catalog{}, nil, err
	}
	if manager.PublicKey == "" || manager.BaseURL == "" {
		return embeddedCatalog, embedded, nil
	}
	catalogURL := strings.TrimSuffix(manager.BaseURL, "/") + "/install/skills/latest/catalog.json"
	fetcher := manager.Fetcher
	if fetcher == nil {
		fetcher = releaseclient.HTTPFetcher{}
	}
	encoded, err := fetcher.Fetch(ctx, catalogURL, MaxCatalogSize)
	if err != nil {
		if allowFallback {
			return embeddedCatalog, embedded, nil
		}
		return Catalog{}, nil, err
	}
	signature, err := fetcher.Fetch(ctx, catalogURL+".sig", ed25519SignatureSize)
	if err != nil {
		if allowFallback {
			return embeddedCatalog, embedded, nil
		}
		return Catalog{}, nil, err
	}
	catalog, err := VerifyCatalog(encoded, signature, manager.PublicKey, catalogURL)
	if err != nil {
		if allowFallback {
			return embeddedCatalog, embedded, nil
		}
		return Catalog{}, nil, err
	}
	if catalog.Revision < embeddedCatalog.Revision {
		return embeddedCatalog, embedded, nil
	}
	return catalog, embedded, nil
}

const ed25519SignatureSize = 64

func (manager Manager) resolveRelease(ctx context.Context, name string, variant CatalogVariant, embedded map[string]EmbeddedRelease, allowFallback bool) (EmbeddedRelease, error) {
	fallback, hasFallback := embedded[name+":"+variant.ID]
	if !hasFallback {
		fallback, hasFallback = embedded[name+":default"]
	}
	if manager.PublicKey == "" || variant.MetadataURL == "" {
		if hasFallback {
			return fallback, nil
		}
		return EmbeddedRelease{}, ErrInvalidRelease
	}
	fetcher := manager.Fetcher
	if fetcher == nil {
		fetcher = releaseclient.HTTPFetcher{}
	}
	encoded, err := fetcher.Fetch(ctx, variant.MetadataURL, MaxMetadataSize)
	if err != nil {
		if allowFallback && hasFallback {
			return fallback, nil
		}
		return EmbeddedRelease{}, err
	}
	signature, err := fetcher.Fetch(ctx, variant.MetadataURL+".sig", ed25519SignatureSize)
	if err != nil {
		if allowFallback && hasFallback {
			return fallback, nil
		}
		return EmbeddedRelease{}, err
	}
	metadata, err := VerifyMetadata(encoded, signature, manager.PublicKey, variant.MetadataURL, name, variant.ID)
	if err != nil {
		if allowFallback && hasFallback {
			return fallback, nil
		}
		return EmbeddedRelease{}, err
	}
	if lessVersion(manager.CurrentVersion, metadata.MinimumCLIVersion) {
		return EmbeddedRelease{}, ErrUnsupportedCLI
	}
	archive, err := fetcher.Fetch(ctx, metadata.Archive.URL, MaxArchiveSize)
	if err != nil || int64(len(archive)) != metadata.Archive.Size ||
		releaseclient.Digest(archive) != metadata.Archive.SHA256 {
		if allowFallback && hasFallback {
			return fallback, nil
		}
		return EmbeddedRelease{}, ErrInvalidRelease
	}
	files, err := ExtractArchive(archive, name)
	if err != nil {
		return EmbeddedRelease{}, err
	}
	return EmbeddedRelease{Metadata: metadata, Files: files}, nil
}

func installFiles(
	destination, agent, scope, version string,
	files map[string][]byte,
	variants ...string,
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
		existing.Name = receipt.Name
		if existing.Name == "" {
			existing.Name = filepath.Base(destination)
		}
		existing.Scope = receipt.Scope
		existing.Variant = receipt.Variant
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
		Name:          filepath.Base(destination),
		Agent:         agent,
		Scope:         scope,
		Path:          destination,
		Version:       version,
		ContentSHA256: kadoskill.Digest(files),
	}
	if len(variants) > 0 {
		item.Variant = variants[0]
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
		(receipt.Name != "" && receipt.Name != item.Name) ||
		(receipt.Variant != "" && receipt.Variant != item.Variant) ||
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
func (manager Manager) reconcileRegistry(value registry, catalog Catalog) (registry, map[string]string) {
	failures := make(map[string]string)
	registered := make(map[string]Installation, len(value.Installations))
	for _, item := range value.Installations {
		registered[item.Path] = item
	}

	nameSet := map[string]bool{}
	for _, skill := range catalog.Skills {
		nameSet[skill.Name] = true
	}
	for _, item := range value.Installations {
		nameSet[item.Name] = true
	}
	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	sort.Strings(names)
	destinations := KnownDestinations(manager.HomeDir, names...)
	agents := make([]string, 0, len(destinations))
	for agent := range destinations {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	seen := make(map[string]struct{}, len(destinations))
	for _, key := range agents {
		destination := destinations[key]
		parts := strings.SplitN(key, ":", 2)
		agent, name := parts[0], parts[1]
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
			(receipt.Name != "" && receipt.Name != name) ||
			receipt.Scope != "user" {
			failures[destination] = "externally_managed"
			continue
		}
		discovered := Installation{
			Name:          name,
			Variant:       receipt.Variant,
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

func installationAt(values []Installation, path string) (Installation, bool) {
	for _, item := range values {
		if item.Path == path {
			return item, true
		}
	}
	return Installation{}, false
}

func upsertTarget(values []Target, item Target) []Target {
	for _, value := range values {
		if value == item {
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
