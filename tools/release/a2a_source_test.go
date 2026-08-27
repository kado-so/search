package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestA2ASourceLockIsClosedAndDisplayOnly(t *testing.T) {
	root := repositoryRoot(t)
	lock, lockPath, err := loadA2ASourceLock(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if lock.Tag != "" || lock.DisplayName != "kado a2a" || len(lock.Patches) != 1 {
		t.Fatalf("unexpected production A2A lock: %#v", lock)
	}
	patch, err := os.ReadFile(filepath.Join(filepath.Dir(lockPath), filepath.FromSlash(lock.Patches[0].Path)))
	if err != nil {
		t.Fatal(err)
	}
	text := string(patch)
	if strings.Count(text, "diff --git ") != 1 ||
		!strings.Contains(text, "diff --git a/internal/cli/root.go b/internal/cli/root.go") ||
		!strings.Contains(text, "cobra.CommandDisplayNameAnnotation: displayName") ||
		!strings.Contains(text, `var displayName = "a2a"`) {
		t.Fatal("production A2A patch is not the bounded display-name adaptation")
	}
}

func TestA2ACommandContractMatchesSourceLock(t *testing.T) {
	root := repositoryRoot(t)
	lock, _, err := loadA2ASourceLock(root, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(filepath.Join(root, "third_party", "a2a-cli", "command-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var contract struct {
		SchemaVersion   string   `json:"schema_version"`
		UpstreamCommit  string   `json:"upstream_commit"`
		UpstreamVersion string   `json:"upstream_version"`
		DisplayName     string   `json:"display_name"`
		GlobalFlags     []string `json:"global_flags"`
		Commands        []struct {
			Path       string   `json:"path"`
			LocalFlags []string `json:"local_flags"`
		} `json:"commands"`
	}
	if err := decoder.Decode(&contract); err != nil {
		t.Fatal(err)
	}
	if contract.SchemaVersion != "kado.a2a-command-contract.v1" ||
		contract.UpstreamCommit != lock.Commit ||
		contract.UpstreamVersion != lock.Version ||
		contract.DisplayName != lock.DisplayName ||
		len(contract.GlobalFlags) == 0 || len(contract.Commands) == 0 {
		t.Fatalf("command contract does not match source lock: %#v", contract)
	}
}

func TestPrepareA2ASourceAcceptsSnapshotAndTaggedLocks(t *testing.T) {
	t.Run("snapshot", func(t *testing.T) {
		fixture := newA2ASourceFixture(t)
		prepared, err := prepareA2ASource(
			fixture.root,
			fixture.source,
			fixture.lockPath,
			filepath.Join(t.TempDir(), "prepared"),
			"go",
		)
		if err != nil {
			t.Fatal(err)
		}
		if prepared.TreeSHA256 != fixture.lock.PatchedTreeSHA256 {
			t.Fatalf("prepared tree = %s", prepared.TreeSHA256)
		}
		if _, err := os.Stat(filepath.Join(prepared.Root, ".git")); !os.IsNotExist(err) {
			t.Fatal("prepared A2A source contains Git history")
		}
	})

	t.Run("tagged", func(t *testing.T) {
		fixture := newA2ASourceFixture(t)
		runTestCommand(t, fixture.source, "git", "tag", "v1.2.3")
		fixture.lock.Tag = "v1.2.3"
		fixture.lock.Version = "1.2.3"
		writeA2ATestLock(t, fixture.lockPath, fixture.lock)
		prepared, err := prepareA2ASource(
			fixture.root,
			fixture.source,
			fixture.lockPath,
			filepath.Join(t.TempDir(), "prepared"),
			"go",
		)
		if err != nil {
			t.Fatal(err)
		}
		if prepared.Lock.Commit != fixture.lock.Commit || prepared.Lock.Tag != "v1.2.3" {
			t.Fatalf("tagged preparation changed build identity: %#v", prepared.Lock)
		}
	})
}

func TestPrepareA2ASourceRejectsSnapshotAfterReleaseTagAppears(t *testing.T) {
	fixture := newA2ASourceFixture(t)
	runTestCommand(t, fixture.source, "git", "tag", "v1.2.3")
	_, err := prepareA2ASource(
		fixture.root,
		fixture.source,
		fixture.lockPath,
		filepath.Join(t.TempDir(), "prepared"),
		"go",
	)
	if err == nil || !strings.Contains(err.Error(), "release tag is available") {
		t.Fatalf("snapshot lock with release tag returned %v", err)
	}
}

func TestPrepareA2ASourceIgnoresHostGitConfiguration(t *testing.T) {
	fixture := newA2ASourceFixture(t)
	configurationDirectory := t.TempDir()
	attributesPath := filepath.Join(configurationDirectory, "attributes")
	if err := os.WriteFile(attributesPath, []byte("* text eol=crlf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := []byte("[core]\n\tautocrlf = true\n\teol = crlf\n\tattributesFile = " + filepath.ToSlash(attributesPath) + "\n")
	configurationPath := filepath.Join(configurationDirectory, "gitconfig")
	if err := os.WriteFile(configurationPath, configuration, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", configurationPath)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")
	t.Setenv("GIT_ATTR_NOSYSTEM", "")

	prepared, err := prepareA2ASource(
		fixture.root,
		fixture.source,
		fixture.lockPath,
		filepath.Join(t.TempDir(), "prepared"),
		"go",
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.TreeSHA256 != fixture.lock.PatchedTreeSHA256 {
		t.Fatalf("prepared tree = %s", prepared.TreeSHA256)
	}
}

func TestPrepareA2ASourceRejectsChangedInputsBeforeUse(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *a2aSourceFixture)
		want   string
	}{
		{
			name: "origin",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				runTestCommand(t, fixture.source, "git", "remote", "set-url", "origin", "https://invalid.example/a2a-cli")
			},
			want: "origin",
		},
		{
			name: "commit",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.Commit = strings.Repeat("0", 40)
				fixture.lock.Version = "0.0.0-20260827.0000000"
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "commit",
		},
		{
			name: "tag",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.Tag = "v1.2.3"
				fixture.lock.Version = "1.2.3"
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "tag",
		},
		{
			name: "archive",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.SourceArchiveSHA256 = strings.Repeat("0", 64)
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "archive",
		},
		{
			name: "source tree",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.SourceTreeSHA256 = strings.Repeat("0", 64)
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "source tree",
		},
		{
			name: "go.mod",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.GoModSHA256 = strings.Repeat("0", 64)
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "go.mod",
		},
		{
			name: "go.sum",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.GoSumSHA256 = strings.Repeat("0", 64)
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "go.sum",
		},
		{
			name: "license",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.License.SHA256 = strings.Repeat("0", 64)
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "license",
		},
		{
			name: "patch digest",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.Patches[0].SHA256 = strings.Repeat("0", 64)
				fixture.lock.PatchSetSHA256 = digestA2APatchSet(fixture.lock.Patches)
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "patch checksum",
		},
		{
			name: "patch applicability",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				patchPath := filepath.Join(filepath.Dir(fixture.lockPath), filepath.FromSlash(a2aPatchPath))
				invalid := []byte("diff --git a/missing.go b/missing.go\n--- a/missing.go\n+++ b/missing.go\n@@ -1 +1 @@\n-old\n+new\n")
				if err := os.WriteFile(patchPath, invalid, 0o644); err != nil {
					t.Fatal(err)
				}
				fixture.lock.Patches[0].SHA256 = digestA2ABytes(invalid)
				fixture.lock.PatchSetSHA256 = digestA2APatchSet(fixture.lock.Patches)
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "does not apply",
		},
		{
			name: "patched tree",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.PatchedTreeSHA256 = strings.Repeat("0", 64)
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "patched A2A source tree",
		},
		{
			name: "toolchain",
			mutate: func(t *testing.T, fixture *a2aSourceFixture) {
				fixture.lock.GoToolchain = "go0.0.1"
				writeA2ATestLock(t, fixture.lockPath, fixture.lock)
			},
			want: "toolchain",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newA2ASourceFixture(t)
			test.mutate(t, &fixture)
			destination := filepath.Join(t.TempDir(), "prepared")
			_, err := prepareA2ASource(fixture.root, fixture.source, fixture.lockPath, destination, "go")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("prepare error = %v, want %q", err, test.want)
			}
			if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
				t.Fatalf("failed preparation left destination behind: %v", statErr)
			}
		})
	}
}

func TestA2ASourceLockRejectsUnknownFields(t *testing.T) {
	fixture := newA2ASourceFixture(t)
	encoded, err := os.ReadFile(fixture.lockPath)
	if err != nil {
		t.Fatal(err)
	}
	encoded = bytes.Replace(encoded, []byte("{\n"), []byte("{\n  \"unexpected\": true,\n"), 1)
	if err := os.WriteFile(fixture.lockPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadA2ASourceLock(fixture.root, fixture.lockPath); err == nil {
		t.Fatal("lock with an unknown field was accepted")
	}
}

func TestProductionA2ASourcePreparation(t *testing.T) {
	source := os.Getenv("KADO_A2A_UPSTREAM_SOURCE")
	if source == "" {
		t.Skip("set KADO_A2A_UPSTREAM_SOURCE to an isolated official checkout")
	}
	root := repositoryRoot(t)
	first, err := prepareA2ASource(root, source, "", filepath.Join(t.TempDir(), "first"), "go")
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepareA2ASource(root, source, "", filepath.Join(t.TempDir(), "second"), "go")
	if err != nil {
		t.Fatal(err)
	}
	if first.TreeSHA256 != second.TreeSHA256 || first.TreeSHA256 != first.Lock.PatchedTreeSHA256 {
		t.Fatalf("prepared identities differ: %s %s", first.TreeSHA256, second.TreeSHA256)
	}
	if diff := compareA2ATrees(t, first.Root, second.Root); len(diff) != 0 {
		t.Fatalf("preparations differ: %v", diff)
	}
	environment := withReleaseEnvironment(os.Environ(), map[string]string{
		"GOTOOLCHAIN": "local",
		"GOFLAGS":     "-mod=readonly",
	})
	command := exec.Command("go", "test", "./internal/cli", "./internal/flagparse", "./internal/output", "./internal/polling")
	command.Dir = first.Root
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("focused upstream tests failed: %v\n%s", err, output)
	}
}

type a2aSourceFixture struct {
	root     string
	source   string
	lockPath string
	lock     a2aSourceLock
}

func newA2ASourceFixture(t *testing.T) a2aSourceFixture {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "upstream")
	if err := os.MkdirAll(filepath.Join(source, "internal", "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"go.mod":               []byte("module github.com/a2aproject/a2a-cli\n\ngo 1.25.0\n"),
		"go.sum":               {},
		"LICENSE":              []byte("Apache License fixture\n"),
		"internal/cli/root.go": []byte("package cli\n\nvar commandName = \"a2a\"\n"),
	}
	for name, value := range files {
		path := filepath.Join(source, filepath.FromSlash(name))
		if err := os.WriteFile(path, value, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runTestCommand(t, source, "git", "init", "--quiet")
	runTestCommand(t, source, "git", "config", "user.name", "Kado Test")
	runTestCommand(t, source, "git", "config", "user.email", "test@kado.invalid")
	runTestCommand(t, source, "git", "remote", "add", "origin", a2aRepository)
	runTestCommand(t, source, "git", "add", ".")
	runTestCommand(t, source, "git", "commit", "--quiet", "-m", "fixture")
	commit := strings.TrimSpace(string(runTestCommand(t, source, "git", "rev-parse", "HEAD")))
	archive := runTestCommand(t, source, "git", "-c", "core.autocrlf=false", "-c", "core.eol=lf", "archive", "--format=tar", commit)
	unpatched := filepath.Join(root, "unpatched")
	if err := os.MkdirAll(unpatched, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractA2AArchive(archive, unpatched); err != nil {
		t.Fatal(err)
	}
	sourceTree, err := digestA2ASourceTree(unpatched)
	if err != nil {
		t.Fatal(err)
	}
	archivedGoMod, err := os.ReadFile(filepath.Join(unpatched, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	archivedGoSum, err := os.ReadFile(filepath.Join(unpatched, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	archivedLicense, err := os.ReadFile(filepath.Join(unpatched, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	lockDirectory := filepath.Join(root, "third_party", "a2a-cli")
	patchPath := filepath.Join(lockDirectory, filepath.FromSlash(a2aPatchPath))
	if err := os.MkdirAll(filepath.Dir(patchPath), 0o755); err != nil {
		t.Fatal(err)
	}
	patch := []byte("diff --git a/internal/cli/root.go b/internal/cli/root.go\n--- a/internal/cli/root.go\n+++ b/internal/cli/root.go\n@@ -1,3 +1,3 @@\n package cli\n \n-var commandName = \"a2a\"\n+var commandName = \"kado a2a\"\n")
	if err := os.WriteFile(patchPath, patch, 0o644); err != nil {
		t.Fatal(err)
	}
	patchLock := a2aLockedFile{Path: a2aPatchPath, SHA256: digestA2ABytes(patch)}
	if _, err := applyA2ASourcePatches(lockDirectory, unpatched, []a2aLockedFile{patchLock}); err != nil {
		t.Fatal(err)
	}
	patchedTree, err := digestA2ASourceTree(unpatched)
	if err != nil {
		t.Fatal(err)
	}
	toolchain := strings.TrimSpace(string(runTestCommand(t, root, "go", "env", "GOVERSION")))
	lock := a2aSourceLock{
		SchemaVersion: a2aSourceLockSchema,
		Repository:    a2aRepository,
		Module:        a2aModule,
		Version:       "0.0.0-20260827." + commit[:7],
		Commit:        commit,
		// Git's raw tar headers vary between Git implementations and versions.
		// Lock the validated archive contents using the canonical extracted-tree
		// digest so the same commit verifies on every release host.
		SourceArchiveSHA256: sourceTree,
		SourceTreeSHA256:    sourceTree,
		PatchedTreeSHA256:   patchedTree,
		GoModSHA256:         digestA2ABytes(archivedGoMod),
		GoSumSHA256:         digestA2ABytes(archivedGoSum),
		License: a2aLicenseLock{
			Path: "LICENSE", SPDX: "Apache-2.0", SHA256: digestA2ABytes(archivedLicense),
		},
		NoticeFiles:    []a2aLockedFile{},
		GoToolchain:    toolchain,
		DisplayName:    a2aDisplayName,
		PatchSetSHA256: digestA2APatchSet([]a2aLockedFile{patchLock}),
		Patches:        []a2aLockedFile{patchLock},
	}
	lockPath := filepath.Join(lockDirectory, "upstream.lock.json")
	writeA2ATestLock(t, lockPath, lock)
	return a2aSourceFixture{root: root, source: source, lockPath: lockPath, lock: lock}
}

func writeA2ATestLock(t *testing.T, path string, lock a2aSourceLock) {
	t.Helper()
	encoded, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runTestCommand(t *testing.T, directory, name string, arguments ...string) []byte {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = os.Environ()
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("%s failed: %v\n%s", name, err, exit.Stderr)
		}
		t.Fatalf("%s failed: %v", name, err)
	}
	return output
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(directory, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root is unavailable: %v", err)
	}
	return root
}

func compareA2ATrees(t *testing.T, left, right string) []string {
	t.Helper()
	manifest := func(root string) map[string]string {
		values := map[string]string{}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			value, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			values[filepath.ToSlash(relative)] = digestA2ABytes(value)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return values
	}
	leftValues := manifest(left)
	rightValues := manifest(right)
	keys := make(map[string]bool, len(leftValues)+len(rightValues))
	for key := range leftValues {
		keys[key] = true
	}
	for key := range rightValues {
		keys[key] = true
	}
	var differences []string
	for key := range keys {
		if leftValues[key] != rightValues[key] {
			differences = append(differences, key)
		}
	}
	sort.Strings(differences)
	return differences
}
