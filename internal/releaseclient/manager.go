package releaseclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/launcher"
)

var (
	ErrInvalidMetadata  = errors.New("release metadata is invalid")
	ErrInvalidSignature = errors.New("release metadata signature is invalid")
	ErrChecksum         = errors.New("release artifact checksum is invalid")
	ErrPlatform         = errors.New("release does not support this platform")
	ErrDowngrade        = errors.New("release downgrade requires explicit permission")
	ErrCandidate        = errors.New("release executable identity is invalid")
	ErrInstall          = errors.New("release could not be installed atomically")
	ErrUninstall        = errors.New("release executable could not be removed")
)

// Fetcher retrieves bounded immutable release assets.
type Fetcher interface {
	Fetch(context.Context, string, int64) ([]byte, error)
}

// HTTPFetcher retrieves HTTPS assets without cookies. It follows one narrowly
// validated redirect from kado.so to Kado's private Azure Blob delivery origin.
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
	clone.Jar = nil
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) != 1 || !allowedAssetRedirect(via[0].URL, request.URL) {
			return http.ErrUseLastResponse
		}
		return nil
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

func allowedAssetRedirect(source, destination *url.URL) bool {
	if source == nil || destination == nil ||
		source.Scheme != "https" ||
		!strings.EqualFold(source.Hostname(), "kado.so") ||
		source.Port() != "" ||
		destination.Scheme != "https" ||
		!strings.EqualFold(
			destination.Hostname(),
			"kadoappassets0c29ff3adb.blob.core.windows.net",
		) ||
		destination.Port() != "" ||
		destination.User != nil ||
		destination.Fragment != "" ||
		!strings.HasPrefix(destination.EscapedPath(), "/app-assets/") {
		return false
	}
	query := destination.Query()
	if query.Get("sp") != "r" ||
		query.Get("spr") != "https" ||
		query.Get("sig") == "" ||
		query.Get("se") == "" ||
		query.Get("sv") == "" ||
		query.Get("sr") != "b" {
		return false
	}
	allowed := map[string]bool{
		"se": true, "sig": true, "ske": true, "skoid": true, "sks": true,
		"skt": true, "sktid": true, "skv": true, "sp": true, "spr": true,
		"sr": true, "st": true, "sv": true,
	}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 || values[0] == "" {
			return false
		}
	}
	return true
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
	LauncherPath   string
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
	Pending     bool
}

// Check verifies current signed metadata without downloading or installing an
// archive.
func (manager Manager) Check(
	ctx context.Context,
	currentVersion string,
) (Result, error) {
	goos, goarch := manager.GOOS, manager.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	result := Result{
		FromVersion: currentVersion,
		Target:      goos + "/" + goarch,
		DryRun:      true,
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
	if _, err := metadata.TargetFor(goos, goarch); err != nil {
		return result, ErrPlatform
	}
	if err := sameReleaseOrigin(metadataURL, metadata); err != nil {
		return result, ErrInvalidMetadata
	}
	result.ToVersion = metadata.Version
	if currentVersion != "" && currentVersion != "dev" {
		comparison, err := compareVersions(metadata.Version, currentVersion)
		if err != nil {
			return result, ErrInvalidMetadata
		}
		result.Changed = comparison > 0
	}
	return result, nil
}

// Update verifies signed metadata, the selected archive, and candidate
// executable identity before replacing anything.
func (manager Manager) Update(ctx context.Context, options Options) (Result, error) {
	if options.LauncherPath != "" && options.TargetPath != options.LauncherPath {
		return Result{}, ErrInstall
	}
	if options.LauncherPath == "" || options.DryRun {
		return manager.update(ctx, options)
	}
	var result Result
	var updateErr error
	lockErr := launcher.WithUpdateLock(options.LauncherPath, func() error {
		if _, activeVersion, err := launcher.ActiveBundle(options.LauncherPath); err == nil {
			options.CurrentVersion = activeVersion
		} else {
			return ErrInstall
		}
		result, updateErr = manager.update(ctx, options)
		return updateErr
	})
	if lockErr != nil {
		if updateErr != nil {
			return result, updateErr
		}
		return result, ErrInstall
	}
	return result, nil
}

func (manager Manager) update(ctx context.Context, options Options) (Result, error) {
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
			if options.LauncherPath == "" {
				return result, ErrInstall
			}
			return result, nil
		}
	}

	archive, err := fetchAndVerify(ctx, fetcher, target.Archive, MaxArchiveSize)
	if err != nil {
		return result, err
	}
	bundle, err := VerifyTargetBundle(target, archive)
	if err != nil {
		return result, err
	}
	if options.TargetPath == "" {
		return result, ErrInstall
	}
	candidate, cleanup, err := writeCandidate(options.TargetPath, bundle.Kado)
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
	if options.LauncherPath != "" {
		err = launcher.InstallBundleVersionLocked(
			options.LauncherPath,
			metadata.Version,
			launcher.ExecutableBundle{Kado: bundle.Kado, A2A: bundle.A2A},
		)
	} else {
		return result, ErrInstall
	}
	if err != nil {
		return result, ErrInstall
	}
	result.Changed = true
	return result, nil
}

// VerifyTargetArchive safely extracts the executable from an archive already
// authenticated by signed release metadata.
func VerifyTargetArchive(target Target, archive []byte) ([]byte, error) {
	binaryName, archiveFormat, ok := targetLayout(target.OS)
	if !ok {
		return nil, ErrChecksum
	}
	binary, err := ExtractBinary(archive, archiveFormat, binaryName)
	if err != nil {
		return nil, ErrChecksum
	}
	return binary, nil
}

// VerifyTargetBundle authenticates and strictly extracts both executables from
// a paired release archive.
func VerifyTargetBundle(target Target, archive []byte) (ExecutableBundle, error) {
	if err := VerifyFile(target.Archive, archive); err != nil {
		return ExecutableBundle{}, ErrChecksum
	}
	binaryName, archiveFormat, ok := targetLayout(target.OS)
	if !ok {
		return ExecutableBundle{}, ErrChecksum
	}
	bundle, err := ExtractBundle(archive, archiveFormat, binaryName)
	if err != nil || target.Sidecar.Size != int64(len(bundle.A2A)) ||
		target.Sidecar.SHA256 != Digest(bundle.A2A) {
		return ExecutableBundle{}, ErrChecksum
	}
	return bundle, nil
}

// Uninstall removes the direct executable pair and managed activation state.
// Credentials and config are deliberately outside this boundary.
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
	_ = os.Remove(filepath.Join(filepath.Dir(targetPath), a2aInstalledName(targetPath)))
	_ = os.Remove(filepath.Join(filepath.Dir(targetPath), "kado.install.json"))
	_ = os.RemoveAll(targetPath + ".d")
	_ = syncDirectory(filepath.Dir(targetPath))
	return nil
}

func a2aInstalledName(targetPath string) string {
	if strings.HasSuffix(strings.ToLower(filepath.Base(targetPath)), ".exe") {
		return "kado-a2a.exe"
	}
	return "kado-a2a"
}

// VerifyExecutable executes only the extracted same-platform candidate and
// checks its stamped, bounded release identity before replacement.
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
	var value buildinfo.VersionReport
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return ErrCandidate
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrCandidate
	}
	publicKey, err := ParsePublicKey(value.Kado.PublicKey)
	if err != nil {
		return ErrCandidate
	}
	publicKeyID, err := KeyID(publicKey)
	if err != nil {
		return ErrCandidate
	}
	a2a := metadata.Components.A2ACLI
	if value.SchemaVersion != buildinfo.VersionSchema ||
		value.Kado.Version != metadata.Version ||
		value.Kado.Commit != metadata.Commit ||
		value.Kado.BuiltAt != metadata.BuiltAt ||
		value.Kado.Target != target.OS+"/"+target.Arch ||
		value.Kado.ReleaseKeyID != metadata.KeyID ||
		value.Components.A2ACLI.Version != a2a.Version ||
		value.Components.A2ACLI.Tag != a2a.Tag ||
		value.Components.A2ACLI.UpstreamCommit != a2a.Commit ||
		value.Components.A2ACLI.BuiltAt != a2a.BuiltAt ||
		value.Components.A2ACLI.Target != target.OS+"/"+target.Arch ||
		value.Components.A2ACLI.PatchSet != "sha256:"+a2a.PatchSetSHA256 ||
		value.Components.A2ACLI.ArtifactSHA256 != target.Sidecar.SHA256 ||
		value.Components.A2ACLI.ArtifactSize != target.Sidecar.Size ||
		publicKeyID != metadata.KeyID {
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
	return manager.replaceExpectedAndVerify(candidate, target, expectedSnapshot, nil)
}

func (manager Manager) replaceExpectedAndVerify(
	candidate string,
	target string,
	expectedSnapshot string,
	verify func() error,
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
	if verify != nil {
		if err := verify(); err != nil {
			rollbackErr := rename(target, candidate)
			if rollbackErr == nil {
				rollbackErr = rename(backup, target)
			}
			if rollbackErr != nil {
				return errors.Join(err, rollbackErr)
			}
			return err
		}
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
	files := make([]File, 0, len(metadata.Targets))
	for _, target := range metadata.Targets {
		files = append(files, target.Archive)
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
