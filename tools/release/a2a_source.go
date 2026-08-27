package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	a2aSourceLockSchema = "kado.a2a-source-lock.v1"
	a2aRepository       = "https://github.com/a2aproject/a2a-cli"
	a2aModule           = "github.com/a2aproject/a2a-cli"
	a2aDefaultLock      = "third_party/a2a-cli/upstream.lock.json"
	a2aPatchPath        = "patches/0001-configurable-display-name.patch"
	a2aDisplayName      = "kado a2a"
	a2aDisplayPatch     = "diff --git a/internal/cli/root.go b/internal/cli/root.go\nindex 5f74d56..3f1acdb 100644\n--- a/internal/cli/root.go\n+++ b/internal/cli/root.go\n@@ -83,0 +84,4 @@ func Execute() int {\n+// displayName changes only Cobra's rendered command path. Official upstream\n+// builds retain \"a2a\"; Kado release builds set this to \"kado a2a\" at link time.\n+var displayName = \"a2a\"\n+\n@@ -88 +92,4 @@ func newRootCmd(cfg *globalConfig, deps deps) *cobra.Command {\n-\t\tUse:           \"a2a\",\n+\t\tUse: \"a2a\",\n+\t\tAnnotations: map[string]string{\n+\t\t\tcobra.CommandDisplayNameAnnotation: displayName,\n+\t\t},\n"
)

var (
	a2aSHA256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	a2aToolchainPattern = regexp.MustCompile(`^go[0-9]+\.[0-9]+\.[0-9]+$`)
	a2aTagPattern       = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
)

type a2aLockedFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type a2aLicenseLock struct {
	Path   string `json:"path"`
	SPDX   string `json:"spdx"`
	SHA256 string `json:"sha256"`
}

type a2aSourceLock struct {
	SchemaVersion       string          `json:"schema_version"`
	Repository          string          `json:"repository"`
	Module              string          `json:"module"`
	Version             string          `json:"version"`
	Commit              string          `json:"commit"`
	Tag                 string          `json:"tag,omitempty"`
	SourceArchiveSHA256 string          `json:"source_archive_sha256"`
	SourceTreeSHA256    string          `json:"source_tree_sha256"`
	PatchedTreeSHA256   string          `json:"patched_tree_sha256"`
	GoModSHA256         string          `json:"go_mod_sha256"`
	GoSumSHA256         string          `json:"go_sum_sha256"`
	License             a2aLicenseLock  `json:"license"`
	NoticeFiles         []a2aLockedFile `json:"notice_files"`
	GoToolchain         string          `json:"go_toolchain"`
	DisplayName         string          `json:"display_name"`
	PatchSetSHA256      string          `json:"patch_set_sha256"`
	Patches             []a2aLockedFile `json:"patches"`
}

type a2aPreparedSource struct {
	Root           string
	Lock           a2aSourceLock
	PatchSetSHA256 string
	TreeSHA256     string
}

func loadA2ASourceLock(root, configured string) (a2aSourceLock, string, error) {
	path := configured
	if path == "" {
		path = a2aDefaultLock
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	within, err := filepath.Rel(root, path)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return a2aSourceLock{}, "", errors.New("A2A source lock must be inside the repository")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return a2aSourceLock{}, "", errors.New("A2A source lock is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var lock a2aSourceLock
	if err := decoder.Decode(&lock); err != nil {
		return a2aSourceLock{}, "", errors.New("A2A source lock is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return a2aSourceLock{}, "", errors.New("A2A source lock must contain one JSON object")
	}
	if err := lock.validate(); err != nil {
		return a2aSourceLock{}, "", err
	}
	return lock, path, nil
}

func (lock a2aSourceLock) validate() error {
	if lock.SchemaVersion != a2aSourceLockSchema ||
		lock.Repository != a2aRepository ||
		lock.Module != a2aModule ||
		!releaseVersionPattern.MatchString(lock.Version) ||
		!commitPattern.MatchString(lock.Commit) ||
		!validA2ASHA256(lock.SourceArchiveSHA256) ||
		!validA2ASHA256(lock.SourceTreeSHA256) ||
		!validA2ASHA256(lock.PatchedTreeSHA256) ||
		!validA2ASHA256(lock.GoModSHA256) ||
		!validA2ASHA256(lock.GoSumSHA256) ||
		lock.License.Path != "LICENSE" ||
		lock.License.SPDX != "Apache-2.0" ||
		!validA2ASHA256(lock.License.SHA256) ||
		!a2aToolchainPattern.MatchString(lock.GoToolchain) ||
		lock.DisplayName != a2aDisplayName ||
		!validA2ASHA256(lock.PatchSetSHA256) ||
		len(lock.Patches) != 1 ||
		lock.Patches[0].Path != a2aPatchPath {
		return errors.New("A2A source lock is invalid")
	}
	if lock.Tag == "" {
		if !strings.Contains(lock.Version, lock.Commit[:7]) {
			return errors.New("A2A snapshot version must identify its commit")
		}
	} else if !a2aTagPattern.MatchString(lock.Tag) || strings.TrimPrefix(lock.Tag, "v") != lock.Version {
		return errors.New("A2A tag and version do not match")
	}
	if err := validateA2ALockedFiles(lock.NoticeFiles, false); err != nil {
		return err
	}
	if err := validateA2ALockedFiles(lock.Patches, true); err != nil {
		return err
	}
	if digestA2APatchSet(lock.Patches) != lock.PatchSetSHA256 {
		return errors.New("A2A patch-set checksum does not match its entries")
	}
	return nil
}

func validateA2ALockedFiles(files []a2aLockedFile, requirePatchDirectory bool) error {
	seen := make(map[string]bool, len(files))
	last := ""
	for _, file := range files {
		clean := filepath.ToSlash(filepath.Clean(file.Path))
		if clean != file.Path || clean == "." || strings.HasPrefix(clean, "../") ||
			filepath.IsAbs(file.Path) || seen[file.Path] || !validA2ASHA256(file.SHA256) {
			return errors.New("A2A locked file entry is invalid")
		}
		if requirePatchDirectory && !strings.HasPrefix(file.Path, "patches/") {
			return errors.New("A2A patches must be inside the lock patch directory")
		}
		if last != "" && file.Path < last {
			return errors.New("A2A locked file entries must be sorted")
		}
		seen[file.Path] = true
		last = file.Path
	}
	return nil
}

func prepareA2ASource(root, sourceRepository, lockPath, destination, goBinary string) (a2aPreparedSource, error) {
	lock, resolvedLockPath, err := loadA2ASourceLock(root, lockPath)
	if err != nil {
		return a2aPreparedSource{}, err
	}
	if sourceRepository == "" || destination == "" || goBinary == "" {
		return a2aPreparedSource{}, errors.New("A2A source preparation paths are required")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return a2aPreparedSource{}, errors.New("A2A source destination must not exist")
	}
	if err := verifyA2ASourceRepository(sourceRepository, lock); err != nil {
		return a2aPreparedSource{}, err
	}
	archive, err := commandOutput(
		sourceRepository,
		a2aGitEnvironment(),
		"git",
		"-c", "core.autocrlf=false",
		"-c", "core.eol=lf",
		"-c", "core.attributesFile="+os.DevNull,
		"archive", "--format=tar", lock.Commit,
	)
	if err != nil {
		return a2aPreparedSource{}, errors.New("A2A source archive could not be created")
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return a2aPreparedSource{}, errors.New("A2A source destination could not be created")
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(destination)
		}
	}()
	if err := extractA2AArchive(archive, destination); err != nil {
		return a2aPreparedSource{}, err
	}
	sourceTree, err := digestA2ASourceTree(destination)
	if err != nil {
		return a2aPreparedSource{}, errors.New("A2A source tree checksum does not match the lock")
	}
	if sourceTree != lock.SourceArchiveSHA256 {
		return a2aPreparedSource{}, errors.New("A2A source archive contents checksum does not match the lock")
	}
	if sourceTree != lock.SourceTreeSHA256 {
		return a2aPreparedSource{}, errors.New("A2A source tree checksum does not match the lock")
	}
	if err := verifyA2ASourceFiles(destination, lock); err != nil {
		return a2aPreparedSource{}, err
	}
	patchSet, err := applyA2ASourcePatches(filepath.Dir(resolvedLockPath), destination, lock.Patches)
	if err != nil {
		return a2aPreparedSource{}, err
	}
	if patchSet != lock.PatchSetSHA256 {
		return a2aPreparedSource{}, errors.New("A2A applied patch set does not match the lock")
	}
	patchedTree, err := digestA2ASourceTree(destination)
	if err != nil {
		return a2aPreparedSource{}, errors.New("patched A2A source tree checksum does not match the lock")
	}
	if patchedTree != lock.PatchedTreeSHA256 {
		return a2aPreparedSource{}, fmt.Errorf("patched A2A source tree checksum does not match the lock: got %s", patchedTree)
	}
	environment := withReleaseEnvironment(sanitizedEnvironment(), map[string]string{
		"GOTOOLCHAIN": "local",
		"GOFLAGS":     "-mod=readonly",
	})
	actualToolchain, err := commandOutput(destination, environment, goBinary, "env", "GOVERSION")
	if err != nil || strings.TrimSpace(string(actualToolchain)) != lock.GoToolchain {
		return a2aPreparedSource{}, fmt.Errorf("A2A source requires pinned Go toolchain %s", lock.GoToolchain)
	}
	if _, err := commandOutput(destination, environment, goBinary, "mod", "verify"); err != nil {
		return a2aPreparedSource{}, errors.New("A2A module checksums could not be verified")
	}
	failed = false
	return a2aPreparedSource{
		Root:           destination,
		Lock:           lock,
		PatchSetSHA256: patchSet,
		TreeSHA256:     patchedTree,
	}, nil
}

func verifyA2ASourceRepository(repository string, lock a2aSourceLock) error {
	environment := a2aGitEnvironment()
	origin, err := commandOutput(repository, environment, "git", "remote", "get-url", "origin")
	if err != nil || normalizeA2ARepository(string(origin)) != lock.Repository {
		return errors.New("A2A source origin does not match the lock")
	}
	commit, err := commandOutput(repository, environment, "git", "rev-parse", lock.Commit+"^{commit}")
	if err != nil || strings.TrimSpace(string(commit)) != lock.Commit {
		return errors.New("A2A source commit is unavailable")
	}
	if lock.Tag != "" {
		resolved, err := commandOutput(repository, environment, "git", "rev-parse", "refs/tags/"+lock.Tag+"^{commit}")
		if err != nil || strings.TrimSpace(string(resolved)) != lock.Commit {
			return errors.New("locked A2A tag does not resolve to the locked commit")
		}
		return nil
	}
	tags, err := commandOutput(repository, environment, "git", "tag", "--points-at", lock.Commit)
	if err != nil {
		return errors.New("A2A source tags could not be inspected")
	}
	for _, tag := range strings.Fields(string(tags)) {
		if a2aTagPattern.MatchString(tag) {
			return errors.New("A2A release tag is available; update the source lock")
		}
	}
	return nil
}

func normalizeA2ARepository(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "/")
	value = strings.TrimSuffix(value, ".git")
	return value
}

func a2aGitEnvironment() []string {
	return withReleaseEnvironment(sanitizedEnvironment(), map[string]string{
		"GIT_ATTR_NOSYSTEM":   "1",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
	})
}

func extractA2AArchive(encoded []byte, destination string) error {
	reader := tar.NewReader(bytes.NewReader(encoded))
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("A2A source archive is invalid")
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			if header.Name != "pax_global_header" {
				return errors.New("A2A source archive contains unexpected global metadata")
			}
			continue
		}
		rawName := header.Name
		if header.Typeflag == tar.TypeDir {
			if !strings.HasSuffix(rawName, "/") || strings.HasSuffix(rawName, "//") {
				return errors.New("A2A source archive directory path is invalid")
			}
			rawName = strings.TrimSuffix(rawName, "/")
		}
		name := filepath.ToSlash(filepath.Clean(rawName))
		if name == "." || name != rawName || strings.Contains(rawName, "\\") ||
			strings.HasPrefix(name, "../") || filepath.IsAbs(header.Name) || seen[name] {
			return errors.New("A2A source archive contains an unsafe or duplicate path")
		}
		seen[name] = true
		path := filepath.Join(destination, filepath.FromSlash(name))
		within, err := filepath.Rel(destination, path)
		if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
			return errors.New("A2A source archive path escapes the destination")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return errors.New("A2A source directory could not be extracted")
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return errors.New("A2A source parent directory could not be extracted")
			}
			mode := fs.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return errors.New("A2A source file could not be extracted")
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				return errors.New("A2A source file could not be extracted")
			}
		default:
			return fmt.Errorf("A2A source archive contains unsupported entry type at %q", name)
		}
	}
}

func verifyA2ASourceFiles(root string, lock a2aSourceLock) error {
	for _, locked := range []struct {
		path   string
		digest string
		label  string
	}{
		{path: "go.mod", digest: lock.GoModSHA256, label: "go.mod"},
		{path: "go.sum", digest: lock.GoSumSHA256, label: "go.sum"},
		{path: lock.License.Path, digest: lock.License.SHA256, label: "license"},
	} {
		value, err := os.ReadFile(filepath.Join(root, locked.path))
		if err != nil || digestA2ABytes(value) != locked.digest {
			return fmt.Errorf("A2A %s checksum does not match the lock", locked.label)
		}
	}
	actualNotices, err := findA2ANoticeFiles(root)
	if err != nil || len(actualNotices) != len(lock.NoticeFiles) {
		return errors.New("A2A notice files do not match the lock")
	}
	for index, path := range actualNotices {
		if lock.NoticeFiles[index].Path != path {
			return errors.New("A2A notice files do not match the lock")
		}
		value, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || digestA2ABytes(value) != lock.NoticeFiles[index].SHA256 {
			return errors.New("A2A notice checksum does not match the lock")
		}
	}
	return nil
}

func findA2ANoticeFiles(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var notices []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(strings.ToUpper(entry.Name()), "NOTICE") {
			notices = append(notices, entry.Name())
		}
	}
	sort.Strings(notices)
	return notices, nil
}

func applyA2ASourcePatches(lockDirectory, sourceRoot string, patches []a2aLockedFile) (string, error) {
	for _, locked := range patches {
		path := filepath.Join(lockDirectory, filepath.FromSlash(locked.Path))
		value, err := os.ReadFile(path)
		if err != nil || bytes.Contains(value, []byte{'\r'}) || digestA2ABytes(value) != locked.SHA256 {
			return "", errors.New("A2A patch checksum does not match the lock")
		}
		if locked.Path != a2aPatchPath || !bytes.Equal(value, []byte(a2aDisplayPatch)) {
			return "", errors.New("A2A display patch does not match the reviewed transformation")
		}
		if err := applyA2ADisplayPatch(sourceRoot); err != nil {
			return "", errors.New("A2A display patch does not apply cleanly")
		}
	}
	return digestA2APatchSet(patches), nil
}

func applyA2ADisplayPatch(sourceRoot string) error {
	path := filepath.Join(sourceRoot, "internal", "cli", "root.go")
	value, err := os.ReadFile(path)
	if err != nil || bytes.Contains(value, []byte{'\r'}) {
		return errors.New("A2A root command source is unavailable")
	}
	executeBoundary := []byte("\treturn 0\n}\n\nfunc newRootCmd(cfg *globalConfig, deps deps) *cobra.Command {")
	patchedBoundary := []byte("\treturn 0\n}\n\n// displayName changes only Cobra's rendered command path. Official upstream\n// builds retain \"a2a\"; Kado release builds set this to \"kado a2a\" at link time.\nvar displayName = \"a2a\"\n\nfunc newRootCmd(cfg *globalConfig, deps deps) *cobra.Command {")
	useField := []byte("\t\tUse:           \"a2a\",\n")
	patchedUseField := []byte("\t\tUse: \"a2a\",\n\t\tAnnotations: map[string]string{\n\t\t\tcobra.CommandDisplayNameAnnotation: displayName,\n\t\t},\n")
	if bytes.Count(value, executeBoundary) != 1 || bytes.Count(value, useField) != 1 {
		return errors.New("A2A root command source does not match the reviewed patch base")
	}
	value = bytes.Replace(value, executeBoundary, patchedBoundary, 1)
	value = bytes.Replace(value, useField, patchedUseField, 1)
	info, err := os.Stat(path)
	if err != nil {
		return errors.New("A2A root command source is unavailable")
	}
	if err := os.WriteFile(path, value, info.Mode().Perm()); err != nil {
		return errors.New("A2A display patch could not be applied")
	}
	return nil
}

func digestA2ASourceTree(root string) (string, error) {
	// Git archive headers and executable-bit round-tripping differ between
	// supported hosts. Entry safety is enforced during extraction; the locked
	// identity intentionally covers canonical paths and bytes only.
	type entry struct {
		path string
		data []byte
	}
	var entries []entry
	err := filepath.WalkDir(root, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || item.IsDir() {
			return nil
		}
		info, err := item.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("A2A source tree contains an unsupported entry")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{path: filepath.ToSlash(relative), data: value})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].path < entries[right].path })
	hash := sha256.New()
	for _, item := range entries {
		if err := binary.Write(hash, binary.BigEndian, uint32(len(item.path))); err != nil {
			return "", err
		}
		_, _ = io.WriteString(hash, item.path)
		if err := binary.Write(hash, binary.BigEndian, uint64(len(item.data))); err != nil {
			return "", err
		}
		_, _ = hash.Write(item.data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func digestA2APatchSet(patches []a2aLockedFile) string {
	hash := sha256.New()
	for _, patch := range patches {
		_, _ = io.WriteString(hash, patch.Path)
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, patch.SHA256)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func digestA2ABytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func validA2ASHA256(value string) bool {
	return a2aSHA256Pattern.MatchString(value)
}

func withReleaseEnvironment(source []string, values map[string]string) []string {
	output := make([]string, 0, len(source)+len(values))
	for _, entry := range source {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, replaced := values[strings.ToUpper(name)]; replaced {
				continue
			}
		}
		output = append(output, entry)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		output = append(output, key+"="+values[key])
	}
	return output
}
