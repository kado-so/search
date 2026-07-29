package skillclient

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const (
	maxSkillFiles = 64
	maxSkillFile  = 4 << 20
)

func ExtractArchive(encoded []byte) (map[string][]byte, error) {
	if len(encoded) == 0 || len(encoded) > MaxArchiveSize {
		return nil, errors.New("skill archive is invalid")
	}
	compressed, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		return nil, errors.New("skill archive is invalid")
	}
	defer compressed.Close()
	reader := tar.NewReader(io.LimitReader(compressed, MaxArchiveSize*4))
	files := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(files) >= maxSkillFiles {
			return nil, errors.New("skill archive is invalid")
		}
		name, ok := safeSkillPath(header.Name)
		if !ok || header.Typeflag != tar.TypeReg ||
			fs.FileMode(header.Mode).Perm() != 0o644 ||
			header.Size <= 0 || header.Size > maxSkillFile {
			return nil, errors.New("skill archive has an unsafe entry")
		}
		if _, duplicate := files[name]; duplicate {
			return nil, errors.New("skill archive has a duplicate path")
		}
		value, err := io.ReadAll(io.LimitReader(reader, maxSkillFile+1))
		if err != nil || int64(len(value)) != header.Size {
			return nil, errors.New("skill archive is invalid")
		}
		files[name] = value
	}
	if _, ok := files["SKILL.md"]; !ok {
		return nil, errors.New("skill archive is missing SKILL.md")
	}
	return files, nil
}

func safeSkillPath(value string) (string, bool) {
	const prefix = SkillName + "/"
	if !strings.HasPrefix(value, prefix) || strings.Contains(value, "\\") {
		return "", false
	}
	name := strings.TrimPrefix(value, prefix)
	if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "/") ||
		name == "." || name == ".." || strings.HasPrefix(name, "../") ||
		name == receiptName || strings.HasPrefix(name, ".kado-") {
		return "", false
	}
	return name, true
}

func sortedFileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
