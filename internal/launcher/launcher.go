// Package launcher selects immutable Kado payloads for direct installations.
package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/kado-so/search/internal/buildinfo"
)

const (
	launcherEnvironment = "KADO_LAUNCHER_PATH"
	payloadEnvironment  = "KADO_PAYLOAD_PATH"
	activationVersionV1 = 1
	activationVersionV2 = 2
	maxActivationSize   = 4096
	activationDigits    = 20
	retainedVersions    = 2
	receiptName         = "kado.install.json"
	maxPayloadSize      = 96 << 20
)

var (
	versionPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	activationPattern = regexp.MustCompile(`^[0-9]{20}\.json$`)
)

// Installation identifies a launcher-managed direct installation.
type Installation struct {
	LauncherPath string
	Root         string
}

type activation struct {
	SchemaVersion int              `json:"schema_version"`
	Generation    uint64           `json:"generation"`
	Version       string           `json:"version"`
	Executable    string           `json:"executable,omitempty"`
	SHA256        string           `json:"sha256,omitempty"`
	Files         *activationFiles `json:"files,omitempty"`
}

type activationFiles struct {
	Kado activationFile `json:"kado"`
	A2A  activationFile `json:"a2a"`
}

type activationFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type receipt struct {
	SchemaVersion int    `json:"schema_version"`
	Channel       string `json:"channel"`
}

// Dispatch runs the payload selected when this invocation starts. It returns
// handled=false when the executable is not a direct launcher installation or
// when the launcher state is unavailable, allowing the embedded CLI to remain
// a safe fallback.
func Dispatch(
	info buildinfo.Info,
	arguments []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) (int, bool) {
	executable, err := currentExecutable()
	if err != nil {
		return 0, false
	}
	if pinned, launcherPath, ok := pinnedPayload(executable); ok {
		if samePath(executable, pinned) {
			return 0, false
		}
		return runPayload(pinned, launcherPath, arguments, stdin, stdout, stderr)
	}
	if info.InstallChannel != "direct" || !installedExecutablePath(executable) || !directInstallation(executable) {
		return 0, false
	}
	cleanupLegacyUpdateFiles(executable)
	payload, _, err := ensureBootstrap(executable, info.Version)
	if err != nil {
		setFallbackEnvironment(executable)
		return 0, false
	}
	return runPayload(payload, executable, arguments, stdin, stdout, stderr)
}

// CurrentInstallation returns the validated stable launcher selected by the
// current payload. Environment values alone are never sufficient: their paths
// must match the running executable and the launcher's managed layout.
func CurrentInstallation() (Installation, bool) {
	executable, err := currentExecutable()
	if err != nil {
		return Installation{}, false
	}
	launcherPath := filepath.Clean(os.Getenv(launcherEnvironment))
	payloadPath := filepath.Clean(os.Getenv(payloadEnvironment))
	if !filepath.IsAbs(launcherPath) || !filepath.IsAbs(payloadPath) || !samePath(executable, payloadPath) {
		return Installation{}, false
	}
	installation := Installation{LauncherPath: launcherPath, Root: managedRoot(launcherPath)}
	if samePath(payloadPath, launcherPath) {
		if validManagedDirectory(installation.Root) != nil {
			return Installation{}, false
		}
		return installation, true
	}
	if !pathWithinVersion(installation, payloadPath) {
		return Installation{}, false
	}
	return installation, true
}

// WithUpdateLock serializes complete update transactions. OS locks disappear
// automatically if an updater is killed, unlike sentinel lock files.
func WithUpdateLock(launcherPath string, action func() error) error {
	if !filepath.IsAbs(launcherPath) || filepath.Clean(launcherPath) != launcherPath {
		return errors.New("launcher path is invalid")
	}
	root := managedRoot(launcherPath)
	if err := ensureManagedDirectory(root); err != nil {
		return err
	}
	return withPlatformLock(root, action)
}

// InstallVersionLocked installs and activates a verified candidate. The caller
// must hold WithUpdateLock for the same launcher path.
func InstallVersionLocked(launcherPath, version, candidate string) error {
	if !validVersion(version) {
		return errors.New("launcher version is invalid")
	}
	root := managedRoot(launcherPath)
	if err := ensureLayout(root); err != nil {
		return err
	}
	value, err := os.ReadFile(candidate)
	if err != nil || len(value) == 0 {
		return errors.New("launcher candidate is unavailable")
	}
	digestValue := digest(value)
	payload, err := installPayload(root, version, value, digestValue)
	if err != nil {
		return err
	}
	current, _, currentErr := activeFromRoot(root)
	if currentErr == nil && samePath(current, payload) {
		return nil
	}
	if err := writeActivation(root, version, digestValue); err != nil {
		return err
	}
	cleanup(root)
	return nil
}

// Active returns the newest complete activation without taking the update
// lock. Activation records are immutable, so concurrent readers see either the
// previous complete record or the new complete record.
func Active(launcherPath string) (string, string, error) {
	return activeFromRoot(managedRoot(launcherPath))
}

func ensureBootstrap(launcherPath, version string) (string, string, error) {
	if !validVersion(version) {
		return "", "", errors.New("launcher version is invalid")
	}
	if payload, activeVersion, err := Active(launcherPath); err == nil {
		return payload, activeVersion, nil
	}
	err := WithUpdateLock(launcherPath, func() error {
		if _, _, err := Active(launcherPath); err == nil {
			return nil
		}
		sidecarPath := filepath.Join(filepath.Dir(launcherPath), a2aExecutableName())
		if _, statErr := os.Lstat(sidecarPath); statErr == nil {
			kado, readErr := readBootstrapExecutable(launcherPath)
			if readErr != nil {
				return readErr
			}
			sidecar, readErr := readBootstrapExecutable(sidecarPath)
			if readErr != nil {
				return readErr
			}
			if err := InstallBundleVersionLocked(
				launcherPath,
				version,
				ExecutableBundle{Kado: kado, A2A: sidecar},
			); err != nil {
				return err
			}
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return errors.New("launcher bootstrap sidecar is unavailable")
		} else if err := InstallVersionLocked(launcherPath, version, launcherPath); err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(launcherPath), receiptName)); err == nil {
			return nil
		}
		return writeDirectReceipt(launcherPath)
	})
	if err != nil {
		return "", "", err
	}
	return Active(launcherPath)
}

func directInstallation(launcherPath string) bool {
	path := filepath.Join(filepath.Dir(launcherPath), receiptName)
	info, statErr := os.Lstat(path)
	if statErr == nil && (!info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0) {
		return false
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	defer file.Close()
	var value receipt
	decoder := json.NewDecoder(io.LimitReader(file, maxActivationSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF) && value.SchemaVersion == 1 && value.Channel == "direct"
}

func writeDirectReceipt(launcherPath string) error {
	directory := filepath.Dir(launcherPath)
	encoded := []byte("{\"schema_version\":1,\"channel\":\"direct\"}\n")
	temporary, err := os.CreateTemp(directory, ".kado-install-")
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
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(encoded)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(directory, receiptName)); err != nil {
		return err
	}
	keep = true
	return syncDirectory(directory)
}

func activeFromRoot(root string) (string, string, error) {
	if err := validManagedDirectory(root); err != nil {
		return "", "", err
	}
	directory := filepath.Join(root, "activations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", "", err
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() > entries[right].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !activationPattern.MatchString(entry.Name()) {
			continue
		}
		record, readErr := readActivation(directory, entry.Name())
		if readErr != nil {
			continue
		}
		payload, valid := validatedActivationPayload(root, record)
		if !valid {
			continue
		}
		return payload, record.Version, nil
	}
	return "", "", errors.New("launcher has no valid activation")
}

func readActivation(directory, name string) (activation, error) {
	file, err := os.Open(filepath.Join(directory, name))
	if err != nil {
		return activation{}, err
	}
	defer file.Close()
	var record activation
	decoder := json.NewDecoder(io.LimitReader(file, maxActivationSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return activation{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return activation{}, errors.New("activation has trailing content")
	}
	generation, err := strconv.ParseUint(strings.TrimSuffix(name, ".json"), 10, 64)
	if err != nil || record.Generation != generation || !validVersion(record.Version) {
		return activation{}, errors.New("activation is invalid")
	}
	switch record.SchemaVersion {
	case activationVersionV1:
		if record.Files != nil || record.Executable != payloadRelativePath(record.Version) ||
			!validDigest(record.SHA256) {
			return activation{}, errors.New("activation is invalid")
		}
	case activationVersionV2:
		if record.Executable != "" || record.SHA256 != "" || record.Files == nil ||
			!validActivationFile(record.Files.Kado, bundleKadoRelativePath(record.Version)) ||
			!validActivationFile(record.Files.A2A, bundleA2ARelativePath(record.Version)) {
			return activation{}, errors.New("activation is invalid")
		}
	default:
		return activation{}, errors.New("activation is invalid")
	}
	return record, nil
}

func readBootstrapExecutable(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxPayloadSize {
		return nil, errors.New("launcher bootstrap executable is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("launcher bootstrap executable is unavailable")
	}
	value, readErr := io.ReadAll(io.LimitReader(file, maxPayloadSize+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(value)) != info.Size() {
		return nil, errors.New("launcher bootstrap executable is unavailable")
	}
	return value, nil
}

func installPayload(root, version string, value []byte, expectedDigest string) (string, error) {
	versions := filepath.Join(root, "versions")
	destinationDirectory := filepath.Join(versions, version)
	destination := filepath.Join(destinationDirectory, executableName())
	if validPayload(root, destination, version) {
		existing, err := os.ReadFile(destination)
		if err == nil && digest(existing) == expectedDigest {
			return destination, nil
		}
	}
	backup := ""
	if _, err := os.Lstat(destinationDirectory); err == nil {
		placeholder, createErr := os.MkdirTemp(versions, ".kado-version-replaced-")
		if createErr != nil {
			return "", createErr
		}
		if removeErr := os.Remove(placeholder); removeErr != nil {
			return "", removeErr
		}
		if renameErr := os.Rename(destinationDirectory, placeholder); renameErr != nil {
			return "", renameErr
		}
		backup = placeholder
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", errors.New("launcher version directory is invalid")
	}
	committed := false
	defer func() {
		if backup == "" {
			return
		}
		if committed {
			_ = os.RemoveAll(backup)
			return
		}
		_ = os.RemoveAll(destinationDirectory)
		_ = os.Rename(backup, destinationDirectory)
		_ = syncDirectory(versions)
	}()
	temporary, err := os.MkdirTemp(versions, ".kado-version-")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	file, err := os.OpenFile(filepath.Join(temporary, executableName()), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return "", err
	}
	if err := file.Chmod(0o755); err == nil {
		_, err = file.Write(value)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(temporary, destinationDirectory); err != nil {
		return "", err
	}
	keep = true
	if err := syncDirectory(versions); err != nil {
		_ = os.RemoveAll(destinationDirectory)
		_ = syncDirectory(versions)
		return "", err
	}
	committed = true
	return destination, nil
}

func writeActivation(root, version, digestValue string) error {
	directory := filepath.Join(root, "activations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	var generation uint64
	for _, entry := range entries {
		if !activationPattern.MatchString(entry.Name()) {
			continue
		}
		value, parseErr := strconv.ParseUint(strings.TrimSuffix(entry.Name(), ".json"), 10, 64)
		if parseErr == nil && value > generation {
			generation = value
		}
	}
	if generation == ^uint64(0) {
		return errors.New("activation generation is exhausted")
	}
	generation++
	record := activation{
		SchemaVersion: activationVersionV1,
		Generation:    generation,
		Version:       version,
		Executable:    payloadRelativePath(version),
		SHA256:        digestValue,
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(directory, ".kado-activation-")
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
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(encoded)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	finalName := fmt.Sprintf("%0*d.json", activationDigits, generation)
	finalPath := filepath.Join(directory, finalName)
	if err := os.Rename(name, finalPath); err != nil {
		return err
	}
	keep = true
	if err := syncDirectory(directory); err != nil {
		_ = os.Remove(finalPath)
		_ = syncDirectory(directory)
		return err
	}
	return nil
}

func cleanup(root string) {
	directory := filepath.Join(root, "activations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	type validRecord struct {
		name    string
		version string
	}
	records := make([]validRecord, 0, len(entries))
	keepVersions := make(map[string]struct{})
	for _, entry := range entries {
		if !activationPattern.MatchString(entry.Name()) {
			continue
		}
		record, readErr := readActivation(directory, entry.Name())
		if readErr != nil {
			_ = os.Remove(filepath.Join(directory, entry.Name()))
			continue
		}
		if _, valid := validatedActivationPayload(root, record); !valid {
			continue
		}
		records = append(records, validRecord{name: entry.Name(), version: record.Version})
	}
	sort.Slice(records, func(left, right int) bool { return records[left].name > records[right].name })
	for index, record := range records {
		if index < retainedVersions {
			keepVersions[record.version] = struct{}{}
			continue
		}
		_ = os.Remove(filepath.Join(directory, record.name))
	}
	versions := filepath.Join(root, "versions")
	versionEntries, err := os.ReadDir(versions)
	if err != nil {
		return
	}
	for _, entry := range versionEntries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".kado-version-") {
			_ = os.RemoveAll(filepath.Join(versions, entry.Name()))
			continue
		}
		if _, retained := keepVersions[entry.Name()]; !retained {
			_ = os.RemoveAll(filepath.Join(versions, entry.Name()))
		}
	}
}

func cleanupLegacyUpdateFiles(launcherPath string) {
	for _, pattern := range []string{".kado-update-helper-*.exe", ".kado-update-payload-*.exe"} {
		matches, err := filepath.Glob(filepath.Join(filepath.Dir(launcherPath), pattern))
		if err != nil {
			continue
		}
		for _, match := range matches {
			_ = os.Remove(match)
		}
	}
}

func ensureLayout(root string) error {
	if err := ensureManagedDirectory(root); err != nil {
		return err
	}
	for _, name := range []string{"versions", "activations"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return errors.New("launcher directory is invalid")
		}
	}
	return nil
}

func ensureManagedDirectory(root string) error {
	if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return validManagedDirectory(root)
}

func validManagedDirectory(root string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return errors.New("managed launcher directory is invalid")
	}
	return nil
}

func validPayload(root, payload, version string) bool {
	if !validVersion(version) || filepath.Clean(payload) != filepath.Join(root, "versions", version, executableName()) {
		return false
	}
	info, err := os.Lstat(payload)
	return err == nil && info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 && info.Size() > 0
}

func pinnedPayload(executable string) (string, string, bool) {
	launcherPath := filepath.Clean(os.Getenv(launcherEnvironment))
	payloadPath := filepath.Clean(os.Getenv(payloadEnvironment))
	if !filepath.IsAbs(launcherPath) || !filepath.IsAbs(payloadPath) {
		return "", "", false
	}
	installation := Installation{LauncherPath: launcherPath, Root: managedRoot(launcherPath)}
	if (samePath(executable, launcherPath) || samePath(executable, payloadPath)) && pathWithinVersion(installation, payloadPath) {
		return payloadPath, launcherPath, true
	}
	return "", "", false
}

func pathWithinVersion(installation Installation, payload string) bool {
	relative, err := filepath.Rel(filepath.Join(installation.Root, "versions"), payload)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	return len(parts) == 2 && validVersion(parts[0]) && parts[1] == executableName() && validPayload(installation.Root, payload, parts[0])
}

func installedExecutablePath(path string) bool {
	if filepath.Base(path) != executableName() {
		return false
	}
	temporary, err := filepath.Abs(os.TempDir())
	if err != nil {
		return true
	}
	relative, err := filepath.Rel(temporary, path)
	return err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func setFallbackEnvironment(path string) {
	_ = os.Setenv(launcherEnvironment, path)
	_ = os.Setenv(payloadEnvironment, path)
}

func managedRoot(launcherPath string) string { return launcherPath + ".d" }

func payloadRelativePath(version string) string {
	return filepath.ToSlash(filepath.Join("versions", version, executableName()))
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "kado.exe"
	}
	return "kado"
}

func validVersion(version string) bool {
	return len(version) <= 48 && versionPattern.MatchString(version)
}

func digest(value []byte) string {
	valueDigest := sha256.Sum256(value)
	return hex.EncodeToString(valueDigest[:])
}

func currentExecutable() (string, error) {
	value, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(value)
}

func samePath(left, right string) bool { return filepath.Clean(left) == filepath.Clean(right) }

func withLaunchEnvironment(environment []string, launcherPath, payloadPath string) []string {
	result := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		if strings.HasPrefix(entry, launcherEnvironment+"=") || strings.HasPrefix(entry, payloadEnvironment+"=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, launcherEnvironment+"="+launcherPath, payloadEnvironment+"="+payloadPath)
}
