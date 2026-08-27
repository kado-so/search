package releaseclient

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"
)

const maxArchiveSupportSize = 16 << 20

const maxBinarySize = 96 << 20

const a2aLicenseName = "LICENSE-A2A-CLI"

// ExecutableBundle contains the two authenticated executables in a Kado
// release archive.
type ExecutableBundle struct {
	Kado []byte
	A2A  []byte
}

// ExtractBinary validates every archive path/type/mode and returns its executable.
func ExtractBinary(
	archive []byte,
	format string,
	binaryName string,
) ([]byte, error) {
	if len(archive) == 0 || len(archive) > MaxArchiveSize {
		return nil, errors.New("release archive is invalid")
	}
	switch format {
	case "tar.gz":
		return extractTarGzip(archive, binaryName)
	case "zip":
		return extractZip(archive, binaryName)
	default:
		return nil, errors.New("release archive format is unsupported")
	}
}

// ExtractBundle validates the exact five-entry paired release contract. The
// legacy ExtractBinary path remains separate for migration compatibility.
func ExtractBundle(
	archive []byte,
	format string,
	binaryName string,
) (ExecutableBundle, error) {
	if len(archive) == 0 || len(archive) > MaxArchiveSize {
		return ExecutableBundle{}, errors.New("release archive is invalid")
	}
	sidecarName, ok := a2aExecutableName(binaryName)
	if !ok {
		return ExecutableBundle{}, errors.New("release executable name is invalid")
	}
	switch format {
	case "tar.gz":
		return extractBundleTarGzip(archive, binaryName, sidecarName)
	case "zip":
		return extractBundleZip(archive, binaryName, sidecarName)
	default:
		return ExecutableBundle{}, errors.New("release archive format is unsupported")
	}
}

func extractBundleTarGzip(
	encoded []byte,
	binaryName string,
	sidecarName string,
) (ExecutableBundle, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		return ExecutableBundle{}, errors.New("release archive is invalid")
	}
	defer compressed.Close()
	reader := tar.NewReader(io.LimitReader(compressed, maxBundleExpandedSize+1))
	values := make(map[string][]byte, 5)
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return ExecutableBundle{}, errors.New("release archive is invalid")
		}
		if !safeArchivePath(header.Name) {
			return ExecutableBundle{}, errors.New("release archive has an unsafe path")
		}
		if _, duplicate := values[header.Name]; duplicate {
			return ExecutableBundle{}, errors.New("release archive has a duplicate path")
		}
		if header.Typeflag != tar.TypeReg {
			return ExecutableBundle{}, errors.New("release archive has an unsafe entry type")
		}
		limit, mode, executable, allowed := bundleEntryPolicy(header.Name, binaryName, sidecarName)
		if !allowed {
			return ExecutableBundle{}, errors.New("release archive has an unexpected entry")
		}
		if fs.FileMode(header.Mode).Perm() != mode || header.Size <= 0 || header.Size > limit {
			return ExecutableBundle{}, errors.New("release archive entry has an unsafe mode or size")
		}
		value, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
		if readErr != nil || int64(len(value)) != header.Size {
			return ExecutableBundle{}, errors.New("release archive entry is invalid")
		}
		if !executable {
			value = nil
		}
		values[header.Name] = value
	}
	return executableBundle(values, binaryName, sidecarName)
}

func extractBundleZip(
	encoded []byte,
	binaryName string,
	sidecarName string,
) (ExecutableBundle, error) {
	reader, err := zip.NewReader(bytes.NewReader(encoded), int64(len(encoded)))
	if err != nil {
		return ExecutableBundle{}, errors.New("release archive is invalid")
	}
	values := make(map[string][]byte, 5)
	for _, file := range reader.File {
		if !safeArchivePath(file.Name) {
			return ExecutableBundle{}, errors.New("release archive has an unsafe path")
		}
		if _, duplicate := values[file.Name]; duplicate {
			return ExecutableBundle{}, errors.New("release archive has a duplicate path")
		}
		if file.FileInfo().IsDir() || file.Mode()&fs.ModeType != 0 {
			return ExecutableBundle{}, errors.New("release archive has an unsafe entry type")
		}
		limit, mode, executable, allowed := bundleEntryPolicy(file.Name, binaryName, sidecarName)
		if !allowed {
			return ExecutableBundle{}, errors.New("release archive has an unexpected entry")
		}
		if file.Mode().Perm() != mode || file.UncompressedSize64 == 0 ||
			file.UncompressedSize64 > uint64(limit) {
			return ExecutableBundle{}, errors.New("release archive entry has an unsafe mode or size")
		}
		opened, openErr := file.Open()
		if openErr != nil {
			return ExecutableBundle{}, errors.New("release archive is invalid")
		}
		value, readErr := io.ReadAll(io.LimitReader(opened, limit+1))
		closeErr := opened.Close()
		if readErr != nil || closeErr != nil || uint64(len(value)) != file.UncompressedSize64 {
			return ExecutableBundle{}, errors.New("release archive entry is invalid")
		}
		if !executable {
			value = nil
		}
		values[file.Name] = value
	}
	return executableBundle(values, binaryName, sidecarName)
}

func bundleEntryPolicy(
	name string,
	binaryName string,
	sidecarName string,
) (int64, fs.FileMode, bool, bool) {
	switch name {
	case binaryName, sidecarName:
		return maxBinarySize, 0o755, true, true
	case "LICENSE", a2aLicenseName, "INSTALL-CLI.md":
		return maxArchiveSupportSize, 0o644, false, true
	default:
		return 0, 0, false, false
	}
}

func executableBundle(
	values map[string][]byte,
	binaryName string,
	sidecarName string,
) (ExecutableBundle, error) {
	for _, name := range []string{
		binaryName,
		sidecarName,
		"LICENSE",
		a2aLicenseName,
		"INSTALL-CLI.md",
	} {
		if _, present := values[name]; !present {
			return ExecutableBundle{}, errors.New("release archive is missing a required entry")
		}
	}
	return ExecutableBundle{Kado: values[binaryName], A2A: values[sidecarName]}, nil
}

func a2aExecutableName(binaryName string) (string, bool) {
	switch binaryName {
	case "kado":
		return "kado-a2a", true
	case "kado.exe":
		return "kado-a2a.exe", true
	default:
		return "", false
	}
}

const maxBundleExpandedSize = 2*maxBinarySize + 3*maxArchiveSupportSize

func extractTarGzip(encoded []byte, binaryName string) ([]byte, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		return nil, errors.New("release archive is invalid")
	}
	defer compressed.Close()
	reader := tar.NewReader(io.LimitReader(compressed, MaxArchiveSize+1))
	var binary []byte
	seen := make(map[string]struct{})
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("release archive is invalid")
		}
		if !safeArchivePath(header.Name) {
			return nil, errors.New("release archive has an unsafe path")
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return nil, errors.New("release archive has a duplicate path")
		}
		seen[header.Name] = struct{}{}
		if header.Typeflag != tar.TypeReg {
			return nil, errors.New("release archive has an unsafe entry type")
		}
		mode := fs.FileMode(header.Mode)
		if header.Name == binaryName {
			if mode.Perm() != 0o755 || header.Size <= 0 ||
				header.Size > maxBinarySize {
				return nil, errors.New("release executable has an unsafe mode or size")
			}
			binary, err = io.ReadAll(io.LimitReader(reader, maxBinarySize+1))
			if err != nil || int64(len(binary)) != header.Size {
				return nil, errors.New("release executable is invalid")
			}
			continue
		}
		if !allowedSupportFile(header.Name) ||
			mode.Perm() != 0o644 ||
			header.Size < 0 ||
			header.Size > maxArchiveSupportSize {
			return nil, errors.New("release archive has an unexpected entry")
		}
		if _, err := io.Copy(io.Discard, io.LimitReader(reader, maxArchiveSupportSize+1)); err != nil {
			return nil, errors.New("release archive is invalid")
		}
	}
	if len(binary) == 0 {
		return nil, errors.New("release executable is missing")
	}
	return binary, nil
}

func extractZip(encoded []byte, binaryName string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(encoded), int64(len(encoded)))
	if err != nil {
		return nil, errors.New("release archive is invalid")
	}
	var binary []byte
	seen := make(map[string]struct{})
	for _, file := range reader.File {
		if !safeArchivePath(file.Name) {
			return nil, errors.New("release archive has an unsafe path")
		}
		if _, duplicate := seen[file.Name]; duplicate {
			return nil, errors.New("release archive has a duplicate path")
		}
		seen[file.Name] = struct{}{}
		if file.FileInfo().IsDir() || file.Mode()&fs.ModeType != 0 {
			return nil, errors.New("release archive has an unsafe entry type")
		}
		mode := file.Mode().Perm()
		if file.Name == binaryName {
			if mode != 0o755 || file.UncompressedSize64 == 0 ||
				file.UncompressedSize64 > maxBinarySize {
				return nil, errors.New("release executable has an unsafe mode or size")
			}
		} else if !allowedSupportFile(file.Name) ||
			mode != 0o644 ||
			file.UncompressedSize64 > maxArchiveSupportSize {
			return nil, errors.New("release archive has an unexpected entry")
		}
		opened, err := file.Open()
		if err != nil {
			return nil, errors.New("release archive is invalid")
		}
		limit := int64(maxArchiveSupportSize)
		if file.Name == binaryName {
			limit = maxBinarySize
		}
		value, readErr := io.ReadAll(io.LimitReader(opened, limit+1))
		closeErr := opened.Close()
		if readErr != nil || closeErr != nil ||
			uint64(len(value)) != file.UncompressedSize64 {
			return nil, errors.New("release archive is invalid")
		}
		if file.Name == binaryName {
			binary = value
		}
	}
	if len(binary) == 0 {
		return nil, errors.New("release executable is missing")
	}
	return binary, nil
}

func safeArchivePath(name string) bool {
	return name != "" &&
		!strings.Contains(name, "\\") &&
		!strings.HasPrefix(name, "/") &&
		path.Clean(name) == name &&
		path.Base(name) == name &&
		name != "." &&
		name != ".."
}

func allowedSupportFile(name string) bool {
	switch name {
	case "LICENSE", "INSTALL-CLI.md":
		return true
	default:
		return false
	}
}
