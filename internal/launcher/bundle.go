package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ExecutableBundle is an authenticated Kado release pair.
type ExecutableBundle struct {
	Kado []byte
	A2A  []byte
}

// BundlePaths identifies both executables selected by one activation.
type BundlePaths struct {
	Kado string
	A2A  string
}

// InstallBundleVersionLocked installs and activates a complete executable
// pair. The caller must hold WithUpdateLock for launcherPath.
func InstallBundleVersionLocked(launcherPath, version string, bundle ExecutableBundle) error {
	if !validVersion(version) || !validBundleBytes(bundle.Kado) || !validBundleBytes(bundle.A2A) {
		return errors.New("launcher bundle is invalid")
	}
	root := managedRoot(launcherPath)
	if err := ensureLayout(root); err != nil {
		return err
	}
	files := activationFiles{
		Kado: activationFile{
			Path: bundleKadoRelativePath(version), Size: int64(len(bundle.Kado)), SHA256: digest(bundle.Kado),
		},
		A2A: activationFile{
			Path: bundleA2ARelativePath(version), Size: int64(len(bundle.A2A)), SHA256: digest(bundle.A2A),
		},
	}
	if err := rejectVersionCollision(root, version, files); err != nil {
		return err
	}
	paths, err := installBundleDirectory(root, version, bundle, files)
	if err != nil {
		return err
	}
	if hasEquivalentBundleActivation(root, version, files) {
		return nil
	}
	if err := writeBundleActivation(root, version, files); err != nil {
		return err
	}
	active, _, err := activeBundleFromRoot(root)
	if err != nil || active != paths {
		return errors.New("launcher bundle activation failed validation")
	}
	cleanup(root)
	return nil
}

// ActiveBundle returns the newest complete activation-v2 pair without taking
// the update lock.
func ActiveBundle(launcherPath string) (BundlePaths, string, error) {
	return activeBundleFromRoot(managedRoot(launcherPath))
}

func activeBundleFromRoot(root string) (BundlePaths, string, error) {
	if err := validManagedDirectory(root); err != nil {
		return BundlePaths{}, "", err
	}
	directory := filepath.Join(root, "activations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return BundlePaths{}, "", err
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() > entries[right].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !activationPattern.MatchString(entry.Name()) {
			continue
		}
		record, err := readActivation(directory, entry.Name())
		if err != nil || record.SchemaVersion != activationVersionV2 {
			continue
		}
		if paths, ok := validatedBundlePaths(root, record); ok {
			return paths, record.Version, nil
		}
	}
	return BundlePaths{}, "", errors.New("launcher has no valid bundle activation")
}

func validatedActivationPayload(root string, record activation) (string, bool) {
	if record.SchemaVersion == activationVersionV2 {
		paths, ok := validatedBundlePaths(root, record)
		return paths.Kado, ok
	}
	payload := filepath.Join(root, filepath.FromSlash(record.Executable))
	if !validPayload(root, payload, record.Version) {
		return "", false
	}
	info, err := os.Lstat(payload)
	if err != nil || info.Size() > maxPayloadSize {
		return "", false
	}
	return payload, validateExecutable(payload, info.Size(), record.SHA256)
}

func validatedBundlePaths(root string, record activation) (BundlePaths, bool) {
	if record.SchemaVersion != activationVersionV2 || record.Files == nil {
		return BundlePaths{}, false
	}
	paths := BundlePaths{
		Kado: filepath.Join(root, filepath.FromSlash(record.Files.Kado.Path)),
		A2A:  filepath.Join(root, filepath.FromSlash(record.Files.A2A.Path)),
	}
	return paths, validateExecutable(paths.Kado, record.Files.Kado.Size, record.Files.Kado.SHA256) &&
		validateExecutable(paths.A2A, record.Files.A2A.Size, record.Files.A2A.SHA256)
}

func validateExecutable(path string, size int64, expectedDigest string) bool {
	if size <= 0 || size > maxPayloadSize || !validDigest(expectedDigest) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 || info.Size() != size {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	value, readErr := io.ReadAll(io.LimitReader(file, size+1))
	closeErr := file.Close()
	return readErr == nil && closeErr == nil && int64(len(value)) == size && digest(value) == expectedDigest
}

func validActivationFile(value activationFile, expectedPath string) bool {
	return value.Path == expectedPath && value.Size > 0 && value.Size <= maxPayloadSize && validDigest(value.SHA256)
}

func validDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func validBundleBytes(value []byte) bool { return len(value) > 0 && len(value) <= maxPayloadSize }

func installBundleDirectory(
	root, version string,
	bundle ExecutableBundle,
	files activationFiles,
) (BundlePaths, error) {
	versions := filepath.Join(root, "versions")
	destination := filepath.Join(versions, version)
	paths := BundlePaths{
		Kado: filepath.Join(destination, executableName()),
		A2A:  filepath.Join(destination, a2aExecutableName()),
	}
	if validateExecutable(paths.Kado, files.Kado.Size, files.Kado.SHA256) &&
		validateExecutable(paths.A2A, files.A2A.Size, files.A2A.SHA256) {
		return paths, nil
	}

	backup := ""
	if _, err := os.Lstat(destination); err == nil {
		placeholder, err := os.MkdirTemp(versions, ".kado-version-replaced-")
		if err != nil {
			return BundlePaths{}, err
		}
		if err := os.Remove(placeholder); err != nil {
			return BundlePaths{}, err
		}
		if err := commitRename(destination, placeholder); err != nil {
			return BundlePaths{}, err
		}
		backup = placeholder
	} else if !errors.Is(err, fs.ErrNotExist) {
		return BundlePaths{}, errors.New("launcher version directory is invalid")
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
		_ = os.RemoveAll(destination)
		_ = commitRename(backup, destination)
		_ = syncDirectory(versions)
	}()

	temporary, err := os.MkdirTemp(versions, ".kado-version-")
	if err != nil {
		return BundlePaths{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := writeBundleExecutable(filepath.Join(temporary, executableName()), bundle.Kado); err != nil {
		return BundlePaths{}, err
	}
	if err := writeBundleExecutable(filepath.Join(temporary, a2aExecutableName()), bundle.A2A); err != nil {
		return BundlePaths{}, err
	}
	if !validateExecutable(filepath.Join(temporary, executableName()), files.Kado.Size, files.Kado.SHA256) ||
		!validateExecutable(filepath.Join(temporary, a2aExecutableName()), files.A2A.Size, files.A2A.SHA256) {
		return BundlePaths{}, errors.New("launcher temporary bundle failed validation")
	}
	if err := commitRename(temporary, destination); err != nil {
		return BundlePaths{}, err
	}
	keep = true
	if err := syncDirectory(versions); err != nil {
		_ = os.RemoveAll(destination)
		_ = syncDirectory(versions)
		return BundlePaths{}, err
	}
	if !validateExecutable(paths.Kado, files.Kado.Size, files.Kado.SHA256) ||
		!validateExecutable(paths.A2A, files.A2A.Size, files.A2A.SHA256) {
		return BundlePaths{}, errors.New("launcher committed bundle failed validation")
	}
	committed = true
	return paths, nil
}

func writeBundleExecutable(path string, value []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o755); err == nil {
		_, err = file.Write(value)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func writeBundleActivation(root, version string, files activationFiles) error {
	directory := filepath.Join(root, "activations")
	generation, err := nextActivationGeneration(directory)
	if err != nil {
		return err
	}
	record := activation{
		SchemaVersion: activationVersionV2,
		Generation:    generation,
		Version:       version,
		Files:         &files,
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
	finalPath := filepath.Join(directory, fmt.Sprintf("%0*d.json", activationDigits, generation))
	if err := commitRename(name, finalPath); err != nil {
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

func nextActivationGeneration(directory string) (uint64, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, err
	}
	var generation uint64
	for _, entry := range entries {
		if !activationPattern.MatchString(entry.Name()) {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSuffix(entry.Name(), ".json"), 10, 64)
		if err == nil && value > generation {
			generation = value
		}
	}
	if generation == ^uint64(0) {
		return 0, errors.New("activation generation is exhausted")
	}
	return generation + 1, nil
}

func hasEquivalentBundleActivation(root, version string, files activationFiles) bool {
	directory := filepath.Join(root, "activations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !activationPattern.MatchString(entry.Name()) {
			continue
		}
		record, err := readActivation(directory, entry.Name())
		if err == nil && record.SchemaVersion == activationVersionV2 && record.Version == version &&
			record.Files != nil && *record.Files == files {
			_, valid := validatedBundlePaths(root, record)
			return valid
		}
	}
	return false
}

func rejectVersionCollision(root, version string, files activationFiles) error {
	directory := filepath.Join(root, "activations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !activationPattern.MatchString(entry.Name()) {
			continue
		}
		record, err := readActivation(directory, entry.Name())
		if err != nil || record.Version != version {
			continue
		}
		if record.SchemaVersion != activationVersionV2 || record.Files == nil || *record.Files != files {
			return errors.New("launcher version collides with an existing activation")
		}
	}
	return nil
}

func bundleKadoRelativePath(version string) string {
	return filepath.ToSlash(filepath.Join("versions", version, executableName()))
}

func bundleA2ARelativePath(version string) string {
	return filepath.ToSlash(filepath.Join("versions", version, a2aExecutableName()))
}

func a2aExecutableName() string {
	if strings.HasSuffix(executableName(), ".exe") {
		return "kado-a2a.exe"
	}
	return "kado-a2a"
}
