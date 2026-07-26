package releaseclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidMetadata  = errors.New("release metadata is invalid")
	ErrInvalidSignature = errors.New("release metadata signature is invalid")
	ErrChecksum         = errors.New("release artifact checksum is invalid")
	ErrPlatform         = errors.New("release does not support this platform")
	ErrDowngrade        = errors.New("release downgrade requires explicit permission")
	ErrProvenance       = errors.New("release provenance is invalid")
	ErrCandidate        = errors.New("release executable provenance is invalid")
	ErrInstall          = errors.New("release could not be installed atomically")
	ErrUninstall        = errors.New("release executable could not be removed")
)

// Fetcher retrieves bounded immutable release assets.
type Fetcher interface {
	Fetch(context.Context, string, int64) ([]byte, error)
}

// HTTPFetcher retrieves HTTPS assets without cookies or redirects.
type HTTPFetcher struct {
	Client *http.Client
}

// Fetch downloads at most limit bytes.
func (fetcher HTTPFetcher) Fetch(
	ctx context.Context,
	rawURL string,
	limit int64,
) ([]byte, error) {
	parsed, err := parseHTTPS(rawURL)
	if err != nil {
		return nil, ErrInvalidMetadata
	}
	client := fetcher.Client
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, ErrInvalidMetadata
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := clone.Do(request)
	if err != nil {
		return nil, errors.New("release download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		response.ContentLength > limit {
		return nil, errors.New("release download failed")
	}
	value, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(value)) > limit {
		return nil, errors.New("release download failed")
	}
	return value, nil
}

// CandidateVerifier proves the extracted executable carries expected metadata.
type CandidateVerifier func(context.Context, string, Metadata, Target) error

// Manager owns one verified install or update transaction.
type Manager struct {
	MetadataURL     string
	PublicKey       string
	GOOS            string
	GOARCH          string
	Fetcher         Fetcher
	VerifyCandidate CandidateVerifier
	rename          func(string, string) error
	remove          func(string) error
	syncDir         func(string) error
}

// Options controls one install/update transaction.
type Options struct {
	TargetPath     string
	CurrentVersion string
	AllowDowngrade bool
	DryRun         bool
}

// Result is safe deterministic update state.
type Result struct {
	FromVersion string
	ToVersion   string
	Target      string
	Changed     bool
	DryRun      bool
}

// Update verifies metadata, signature, checksums, provenance, platform, and
// candidate executable before replacing anything.
func (manager Manager) Update(ctx context.Context, options Options) (Result, error) {
	goos, goarch := manager.GOOS, manager.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	result := Result{
		FromVersion: options.CurrentVersion,
		Target:      goos + "/" + goarch,
		DryRun:      options.DryRun,
	}
	targetSnapshot := ""
	if !options.DryRun {
		var snapshotErr error
		targetSnapshot, snapshotErr = snapshotExecutable(options.TargetPath)
		if snapshotErr != nil {
			return result, ErrInstall
		}
	}
	fetcher := manager.Fetcher
	if fetcher == nil {
		fetcher = HTTPFetcher{}
	}
	metadataURL, err := parseHTTPS(manager.MetadataURL)
	if err != nil {
		return result, ErrInvalidMetadata
	}
	metadataBytes, err := fetcher.Fetch(ctx, metadataURL.String(), MaxMetadataSize)
	if err != nil {
		return result, err
	}
	signatureBytes, err := fetcher.Fetch(
		ctx,
		metadataURL.String()+".sig",
		ed25519SignatureSize,
	)
	if err != nil {
		return result, err
	}
	metadata, err := VerifyMetadata(metadataBytes, signatureBytes, manager.PublicKey)
	if err != nil {
		if errors.Is(err, errInvalidSignature) {
			return result, ErrInvalidSignature
		}
		return result, ErrInvalidMetadata
	}
	result.ToVersion = metadata.Version
	target, err := metadata.TargetFor(goos, goarch)
	if err != nil {
		return result, ErrPlatform
	}
	if err := sameReleaseOrigin(metadataURL, metadata); err != nil {
		return result, ErrInvalidMetadata
	}
	if options.CurrentVersion != "" && options.CurrentVersion != "dev" {
		comparison, err := compareVersions(metadata.Version, options.CurrentVersion)
		if err != nil {
			return result, ErrInvalidMetadata
		}
		if comparison < 0 && !options.AllowDowngrade {
			return result, ErrDowngrade
		}
		if comparison == 0 && !options.DryRun {
			return result, nil
		}
	}

	provenance, err := fetchAndVerify(
		ctx,
		fetcher,
		metadata.Provenance,
		MaxSupportSize,
	)
	if err != nil {
		return result, err
	}
	sbom, err := fetchAndVerify(ctx, fetcher, target.SBOM, MaxSupportSize)
	if err != nil {
		return result, err
	}
	archive, err := fetchAndVerify(ctx, fetcher, target.Archive, MaxArchiveSize)
	if err != nil {
		return result, err
	}
	binary, err := VerifyTargetArtifacts(
		metadata,
		target,
		provenance,
		sbom,
		archive,
	)
	if err != nil {
		return result, err
	}
	if options.TargetPath == "" {
		return result, ErrInstall
	}
	candidate, cleanup, err := writeCandidate(options.TargetPath, binary)
	if err != nil {
		return result, ErrInstall
	}
	defer cleanup()
	verifier := manager.VerifyCandidate
	if verifier == nil {
		verifier = VerifyExecutable
	}
	if err := verifier(ctx, candidate, metadata, target); err != nil {
		return result, ErrCandidate
	}
	if options.DryRun {
		return result, nil
	}
	if err := manager.replaceExpected(
		candidate,
		options.TargetPath,
		targetSnapshot,
	); err != nil {
		return result, ErrInstall
	}
	result.Changed = true
	return result, nil
}

// VerifyTargetArtifacts proves the signed provenance, SBOM, archive, and
// extracted executable agree for one target.
func VerifyTargetArtifacts(
	metadata Metadata,
	target Target,
	provenance []byte,
	sbom []byte,
	archive []byte,
) ([]byte, error) {
	if err := verifyProvenance(provenance, metadata, target); err != nil {
		return nil, ErrProvenance
	}
	if err := verifySBOM(sbom, metadata, target); err != nil {
		return nil, ErrProvenance
	}
	binary, err := ExtractBinary(archive, target.ArchiveFormat, target.BinaryName)
	if err != nil || VerifyFile(target.Binary, binary) != nil {
		return nil, ErrChecksum
	}
	return binary, nil
}

// Uninstall removes only the selected executable. Credentials and config are
// deliberately outside this boundary.
func Uninstall(targetPath string) error {
	if err := validateTargetPath(targetPath, true); err != nil {
		return ErrUninstall
	}
	releaseLock, err := acquireUpdateLock(targetPath)
	if err != nil {
		return ErrUninstall
	}
	defer releaseLock()
	if err := validateTargetPath(targetPath, true); err != nil {
		return ErrUninstall
	}
	if err := os.Remove(targetPath); err != nil {
		return ErrUninstall
	}
	_ = syncDirectory(filepath.Dir(targetPath))
	return nil
}

// VerifyExecutable executes only the extracted same-platform candidate and
// checks its stamped, bounded provenance before replacement.
func VerifyExecutable(
	ctx context.Context,
	candidate string,
	metadata Metadata,
	target Target,
) error {
	verifyContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(verifyContext, candidate, "version", "--json")
	var stdout limitedBuffer
	stdout.limit = 4096
	command.Stdout = &stdout
	command.Stderr = io.Discard
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	if err := command.Run(); err != nil {
		return ErrCandidate
	}
	var value struct {
		Version      string `json:"version"`
		Commit       string `json:"commit"`
		BuiltAt      string `json:"built_at"`
		Target       string `json:"target"`
		ReleaseKeyID string `json:"release_key_id"`
		PublicKey    string `json:"release_public_key"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return ErrCandidate
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrCandidate
	}
	if value.Version != metadata.Version ||
		value.Commit != metadata.Commit ||
		value.BuiltAt != metadata.BuiltAt ||
		value.Target != target.OS+"/"+target.Arch ||
		value.ReleaseKeyID != metadata.KeyID ||
		value.PublicKey != metadata.SigningPublicKey {
		return ErrCandidate
	}
	return nil
}

func (manager Manager) replace(candidate, target string) error {
	expected, err := snapshotExecutable(target)
	if err != nil {
		return err
	}
	return manager.replaceExpected(candidate, target, expected)
}

func (manager Manager) replaceExpected(
	candidate string,
	target string,
	expectedSnapshot string,
) error {
	releaseLock, err := acquireUpdateLock(target)
	if err != nil {
		return err
	}
	defer releaseLock()
	currentSnapshot, err := snapshotExecutable(target)
	if err != nil || currentSnapshot != expectedSnapshot {
		return ErrInstall
	}
	rename := manager.rename
	if rename == nil {
		rename = os.Rename
	}
	remove := manager.remove
	if remove == nil {
		remove = os.Remove
	}
	syncDir := manager.syncDir
	if syncDir == nil {
		syncDir = syncDirectory
	}
	_, statErr := os.Lstat(target)
	if errors.Is(statErr, fs.ErrNotExist) {
		if err := rename(candidate, target); err != nil {
			return err
		}
		if err := syncDir(filepath.Dir(target)); err != nil {
			rollbackErr := rename(target, candidate)
			if rollbackErr != nil {
				return errors.Join(err, rollbackErr)
			}
			return err
		}
		return nil
	}
	if statErr != nil || validateTargetPath(target, true) != nil {
		return ErrInstall
	}
	backupHandle, err := os.CreateTemp(filepath.Dir(target), ".kado-rollback-*")
	if err != nil {
		return err
	}
	backup := backupHandle.Name()
	placeholderNeedsCleanup := true
	defer func() {
		if placeholderNeedsCleanup {
			_ = remove(backup)
		}
	}()
	if err := backupHandle.Close(); err != nil {
		return err
	}
	if err := remove(backup); err != nil {
		return err
	}
	placeholderNeedsCleanup = false
	if err := rename(target, backup); err != nil {
		return err
	}
	if err := rename(candidate, target); err != nil {
		if rollbackErr := rename(backup, target); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	if err := syncDir(filepath.Dir(target)); err != nil {
		rollbackErr := rename(target, candidate)
		if rollbackErr == nil {
			rollbackErr = rename(backup, target)
		}
		if rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	backupNeedsCleanup := true
	defer func() {
		if backupNeedsCleanup {
			_ = remove(backup)
		}
	}()
	if err := remove(backup); err != nil {
		return err
	}
	backupNeedsCleanup = false
	return syncDir(filepath.Dir(target))
}

func snapshotExecutable(target string) (string, error) {
	if target == "" {
		return "", ErrInstall
	}
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return "missing", nil
	}
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode()&fs.ModeSymlink != 0 ||
		info.Size() <= 0 ||
		info.Size() > maxBinarySize {
		return "", ErrInstall
	}
	value, err := os.ReadFile(target)
	if err != nil || int64(len(value)) != info.Size() {
		return "", ErrInstall
	}
	return Digest(value), nil
}

func acquireUpdateLock(target string) (func(), error) {
	lockPath := target + ".update.lock"
	lock, err := os.OpenFile(
		lockPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return nil, ErrInstall
	}
	if _, err := fmt.Fprintf(lock, "%d\n", os.Getpid()); err != nil {
		_ = lock.Close()
		_ = os.Remove(lockPath)
		return nil, ErrInstall
	}
	if err := lock.Sync(); err != nil {
		_ = lock.Close()
		_ = os.Remove(lockPath)
		return nil, ErrInstall
	}
	if err := lock.Close(); err != nil {
		_ = os.Remove(lockPath)
		return nil, ErrInstall
	}
	return func() {
		_ = os.Remove(lockPath)
		_ = syncDirectory(filepath.Dir(target))
	}, nil
}

func writeCandidate(target string, binary []byte) (string, func(), error) {
	if err := validateTargetPath(target, false); err != nil {
		return "", func() {}, err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".kado-candidate-*")
	if err != nil {
		return "", func() {}, err
	}
	name := file.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := file.Chmod(0o755); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if _, err := file.Write(binary); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return name, cleanup, nil
}

func validateTargetPath(target string, mustExist bool) error {
	if !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return ErrInstall
	}
	parent := filepath.Dir(target)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&fs.ModeSymlink != 0 {
		return ErrInstall
	}
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) && !mustExist {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return ErrInstall
	}
	return nil
}

func fetchAndVerify(
	ctx context.Context,
	fetcher Fetcher,
	file File,
	limit int64,
) ([]byte, error) {
	value, err := fetcher.Fetch(ctx, file.URL, limit)
	if err != nil {
		return nil, err
	}
	if err := VerifyFile(file, value); err != nil {
		return nil, ErrChecksum
	}
	return value, nil
}

func sameReleaseOrigin(metadataURL *url.URL, metadata Metadata) error {
	files := []File{
		metadata.Checksums,
		metadata.Provenance,
		metadata.InstallGuide,
		metadata.InstallUnix,
		metadata.InstallPower,
		metadata.UninstallUnix,
		metadata.UninstallPower,
	}
	for _, target := range metadata.Targets {
		files = append(files, target.Binary, target.Archive, target.SBOM)
	}
	for _, file := range files {
		parsed, err := parseHTTPS(file.URL)
		if err != nil ||
			parsed.Scheme != metadataURL.Scheme ||
			!strings.EqualFold(parsed.Host, metadataURL.Host) {
			return ErrInvalidMetadata
		}
	}
	return nil
}

func verifyProvenance(encoded []byte, metadata Metadata, target Target) error {
	var statement struct {
		Type          string `json:"_type"`
		PredicateType string `json:"predicateType"`
		Subject       []struct {
			Name   string            `json:"name"`
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
		Predicate struct {
			BuildDefinition struct {
				BuildType          string `json:"buildType"`
				ExternalParameters struct {
					Version string `json:"version"`
					Commit  string `json:"commit"`
				} `json:"externalParameters"`
				InternalParameters   map[string]any `json:"internalParameters"`
				ResolvedDependencies []struct {
					URI    string            `json:"uri"`
					Digest map[string]string `json:"digest"`
				} `json:"resolvedDependencies"`
			} `json:"buildDefinition"`
			RunDetails struct {
				Builder struct {
					ID string `json:"id"`
				} `json:"builder"`
				Metadata struct {
					InvocationID string `json:"invocationId"`
					StartedOn    string `json:"startedOn"`
					FinishedOn   string `json:"finishedOn"`
				} `json:"metadata"`
				Byproducts []any `json:"byproducts"`
			} `json:"runDetails"`
		} `json:"predicate"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&statement); err != nil {
		return ErrProvenance
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrProvenance
	}
	if statement.Type != "https://in-toto.io/Statement/v1" ||
		statement.PredicateType != "https://slsa.dev/provenance/v1" ||
		statement.Predicate.BuildDefinition.BuildType !=
			"https://kado.so/build-types/go-cli-release/v1" ||
		statement.Predicate.BuildDefinition.ExternalParameters.Version != metadata.Version ||
		statement.Predicate.BuildDefinition.ExternalParameters.Commit != metadata.Commit ||
		len(statement.Predicate.BuildDefinition.InternalParameters) != 0 ||
		len(statement.Predicate.BuildDefinition.ResolvedDependencies) != 1 ||
		statement.Predicate.BuildDefinition.ResolvedDependencies[0].URI !=
			"git+"+metadata.Repository+"@"+metadata.Commit ||
		statement.Predicate.BuildDefinition.ResolvedDependencies[0].
			Digest["gitCommit"] != metadata.Commit ||
		statement.Predicate.RunDetails.Builder.ID !=
			"https://github.com/kado-so/search/.github/workflows/cli-release.yml" ||
		statement.Predicate.RunDetails.Metadata.InvocationID !=
			metadata.Version+"@"+metadata.Commit ||
		statement.Predicate.RunDetails.Metadata.StartedOn != metadata.BuiltAt ||
		statement.Predicate.RunDetails.Metadata.FinishedOn != metadata.BuiltAt ||
		len(statement.Predicate.RunDetails.Byproducts) != 0 {
		return ErrProvenance
	}
	subjects := make(map[string]string, len(statement.Subject))
	for _, subject := range statement.Subject {
		if len(subject.Digest) != 1 || subject.Digest["sha256"] == "" {
			return ErrProvenance
		}
		subjects[subject.Name] = subject.Digest["sha256"]
	}
	for _, file := range []File{target.Binary, target.Archive, target.SBOM} {
		if subjects[file.Name] != file.SHA256 {
			return ErrProvenance
		}
	}
	return nil
}

func verifySBOM(encoded []byte, metadata Metadata, target Target) error {
	var document struct {
		SPDXVersion string `json:"spdxVersion"`
		Name        string `json:"name"`
		Comment     string `json:"comment"`
		Packages    []struct {
			Name    string `json:"name"`
			Version string `json:"versionInfo"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil ||
		document.SPDXVersion != "SPDX-2.3" ||
		document.Name != target.SBOM.Name ||
		document.Comment != target.OS+"/"+target.Arch ||
		len(document.Packages) == 0 ||
		document.Packages[0].Name != Product ||
		document.Packages[0].Version != metadata.Version {
		return ErrProvenance
	}
	return nil
}

func compareVersions(left, right string) (int, error) {
	leftParts, leftPre, err := numericVersion(left)
	if err != nil {
		return 0, err
	}
	rightParts, rightPre, err := numericVersion(right)
	if err != nil {
		return 0, err
	}
	for index := range leftParts {
		if leftParts[index] < rightParts[index] {
			return -1, nil
		}
		if leftParts[index] > rightParts[index] {
			return 1, nil
		}
	}
	if leftPre == rightPre {
		return 0, nil
	}
	if leftPre == "" {
		return 1, nil
	}
	if rightPre == "" {
		return -1, nil
	}
	leftIdentifiers := strings.Split(leftPre, ".")
	rightIdentifiers := strings.Split(rightPre, ".")
	for index := 0; index < len(leftIdentifiers) && index < len(rightIdentifiers); index++ {
		comparison := comparePrereleaseIdentifier(
			leftIdentifiers[index],
			rightIdentifiers[index],
		)
		if comparison != 0 {
			return comparison, nil
		}
	}
	switch {
	case len(leftIdentifiers) < len(rightIdentifiers):
		return -1, nil
	case len(leftIdentifiers) > len(rightIdentifiers):
		return 1, nil
	default:
		return 0, nil
	}
}

func comparePrereleaseIdentifier(left, right string) int {
	leftNumber, leftErr := strconv.ParseUint(left, 10, 64)
	rightNumber, rightErr := strconv.ParseUint(right, 10, 64)
	switch {
	case leftErr == nil && rightErr == nil:
		if leftNumber < rightNumber {
			return -1
		}
		if leftNumber > rightNumber {
			return 1
		}
		return 0
	case leftErr == nil:
		return -1
	case rightErr == nil:
		return 1
	default:
		return strings.Compare(left, right)
	}
}

func numericVersion(value string) ([3]uint64, string, error) {
	var output [3]uint64
	if !versionPattern.MatchString(value) {
		return output, "", ErrInvalidMetadata
	}
	core := strings.SplitN(value, "+", 2)[0]
	parts := strings.SplitN(core, "-", 2)
	numbers := strings.Split(parts[0], ".")
	for index, number := range numbers {
		parsed, err := strconv.ParseUint(number, 10, 64)
		if err != nil {
			return output, "", ErrInvalidMetadata
		}
		output[index] = parsed
	}
	pre := ""
	if len(parts) == 2 {
		pre = parts[1]
	}
	return output, pre, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	if buffer.Len()+len(value) > buffer.limit {
		return 0, errors.New("release candidate output exceeded limit")
	}
	return buffer.Buffer.Write(value)
}

const ed25519SignatureSize = 64
