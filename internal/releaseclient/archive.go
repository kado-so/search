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

const maxBinarySize = 96 << 20

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
			header.Size > MaxSupportSize {
			return nil, errors.New("release archive has an unexpected entry")
		}
		if _, err := io.Copy(io.Discard, io.LimitReader(reader, MaxSupportSize+1)); err != nil {
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
			file.UncompressedSize64 > MaxSupportSize {
			return nil, errors.New("release archive has an unexpected entry")
		}
		opened, err := file.Open()
		if err != nil {
			return nil, errors.New("release archive is invalid")
		}
		limit := int64(MaxSupportSize)
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
