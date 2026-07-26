package releaseclient

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"
)

func TestCleanInstallUpdateDowngradeDryRunAndUninstall(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	targetPath := filepath.Join(root, executableName(runtime.GOOS))
	credential := filepath.Join(root, "credential-record")
	if err := os.WriteFile(credential, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}

	first := newReleaseFixture(t, "0.1.0", runtime.GOOS, runtime.GOARCH, false)
	manager := first.manager(t)
	result, err := manager.Update(context.Background(), Options{
		TargetPath: targetPath,
	})
	if err != nil || !result.Changed || result.ToVersion != "0.1.0" {
		t.Fatalf("first install result=%#v error=%v", result, err)
	}
	assertFileContent(t, targetPath, first.binary)

	second := newReleaseFixture(t, "0.2.0", runtime.GOOS, runtime.GOARCH, false)
	manager = second.manager(t)
	result, err = manager.Update(context.Background(), Options{
		TargetPath:     targetPath,
		CurrentVersion: "0.1.0",
		DryRun:         true,
	})
	if err != nil || result.Changed || !result.DryRun {
		t.Fatalf("dry run result=%#v error=%v", result, err)
	}
	assertFileContent(t, targetPath, first.binary)

	result, err = manager.Update(context.Background(), Options{
		TargetPath:     targetPath,
		CurrentVersion: "0.1.0",
	})
	if err != nil || !result.Changed {
		t.Fatalf("update result=%#v error=%v", result, err)
	}
	assertFileContent(t, targetPath, second.binary)

	downgrade := first.manager(t)
	if _, err := downgrade.Update(context.Background(), Options{
		TargetPath:     targetPath,
		CurrentVersion: "0.2.0",
	}); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("downgrade error = %v", err)
	}
	if _, err := downgrade.Update(context.Background(), Options{
		TargetPath:     targetPath,
		CurrentVersion: "0.2.0",
		AllowDowngrade: true,
	}); err != nil {
		t.Fatalf("explicit downgrade error = %v", err)
	}
	assertFileContent(t, targetPath, first.binary)

	if err := Uninstall(targetPath); err != nil {
		t.Fatalf("Uninstall error = %v", err)
	}
	if _, err := os.Stat(targetPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("executable still exists: %v", err)
	}
	assertFileContent(t, credential, []byte("preserve"))
}

func TestUpdateRejectsTamperSignatureChecksumProvenanceAndPlatform(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*releaseFixture)
		want   error
	}{
		{
			name: "metadata tamper",
			mutate: func(fixture *releaseFixture) {
				fixture.fetch[fixture.metadataURL][10] ^= 1
			},
			want: ErrInvalidSignature,
		},
		{
			name: "signature tamper",
			mutate: func(fixture *releaseFixture) {
				fixture.fetch[fixture.metadataURL+".sig"][0] ^= 1
			},
			want: ErrInvalidSignature,
		},
		{
			name: "archive checksum",
			mutate: func(fixture *releaseFixture) {
				fixture.fetch[fixture.target.Archive.URL][0] ^= 1
			},
			want: ErrChecksum,
		},
		{
			name: "semantic provenance",
			mutate: func(fixture *releaseFixture) {
				fixture = resignInvalidProvenance(fixture)
			},
			want: ErrProvenance,
		},
		{
			name: "unsupported platform",
			mutate: func(fixture *releaseFixture) {
				fixture.goos = "freebsd"
				fixture.goarch = "amd64"
			},
			want: ErrPlatform,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseFixture(
				t,
				"0.2.0",
				runtime.GOOS,
				runtime.GOARCH,
				false,
			)
			test.mutate(fixture)
			manager := fixture.manager(t)
			_, err := manager.Update(context.Background(), Options{
				TargetPath: filepath.Join(t.TempDir(), executableName(runtime.GOOS)),
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("Update error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDryRunVerifiesCurrentVersionArtifactsInsteadOfShortCircuiting(t *testing.T) {
	t.Parallel()

	fixture := newReleaseFixture(t, "0.2.0", runtime.GOOS, runtime.GOARCH, false)
	fixture.fetch[fixture.target.Archive.URL][0] ^= 1
	_, err := fixture.manager(t).Update(context.Background(), Options{
		TargetPath:     filepath.Join(t.TempDir(), executableName(runtime.GOOS)),
		CurrentVersion: "0.2.0",
		DryRun:         true,
	})
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("dry-run error = %v, want checksum failure", err)
	}
}

func TestArchiveRejectsTraversalSymlinkWrongModeAndDuplicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
		value  []byte
	}{
		{
			name:   "tar traversal",
			format: "tar.gz",
			value: makeTestTar(t, []tarEntry{
				{name: "../kado", mode: 0o755, value: []byte("binary")},
			}),
		},
		{
			name:   "tar symlink",
			format: "tar.gz",
			value: makeTestTar(t, []tarEntry{
				{name: "kado", mode: 0o755, typeflag: tar.TypeSymlink},
			}),
		},
		{
			name:   "tar wrong mode",
			format: "tar.gz",
			value: makeTestTar(t, []tarEntry{
				{name: "kado", mode: 0o777, value: []byte("binary")},
			}),
		},
		{
			name:   "zip duplicate",
			format: "zip",
			value: makeTestZip(t, []zipEntry{
				{name: "kado.exe", mode: 0o755, value: []byte("first")},
				{name: "kado.exe", mode: 0o755, value: []byte("second")},
			}),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			binary := "kado"
			if test.format == "zip" {
				binary = "kado.exe"
			}
			if _, err := ExtractBinary(test.value, test.format, binary); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestAtomicReplacementFaultsNeverInstallEmptyOrCorruptBinary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		failRemove    int
		failRename    int
		failSync      int
		wantTarget    []byte
		wantCandidate bool
	}{
		{
			name:          "rollback placeholder removal before replacement",
			failRemove:    1,
			wantTarget:    []byte("old"),
			wantCandidate: true,
		},
		{
			name:          "installed binary to rollback rename",
			failRename:    1,
			wantTarget:    []byte("old"),
			wantCandidate: true,
		},
		{
			name:          "candidate to installed binary rename",
			failRename:    2,
			wantTarget:    []byte("old"),
			wantCandidate: true,
		},
		{
			name:          "installed candidate directory sync",
			failSync:      1,
			wantTarget:    []byte("old"),
			wantCandidate: true,
		},
		{
			name:       "obsolete rollback removal",
			failRemove: 2,
			wantTarget: []byte("new"),
		},
		{
			name:       "rollback removal directory sync",
			failSync:   2,
			wantTarget: []byte("new"),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			target := filepath.Join(root, "kado")
			candidate := filepath.Join(root, ".candidate")
			if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(candidate, []byte("new"), 0o755); err != nil {
				t.Fatal(err)
			}
			removeCalls, renameCalls, syncCalls := 0, 0, 0
			manager := Manager{
				remove: func(path string) error {
					removeCalls++
					if removeCalls == test.failRemove {
						return errors.New("injected remove failure")
					}
					return os.Remove(path)
				},
				rename: func(old, new string) error {
					renameCalls++
					if renameCalls == test.failRename {
						return errors.New("injected rename failure")
					}
					return os.Rename(old, new)
				},
				syncDir: func(path string) error {
					syncCalls++
					if syncCalls == test.failSync {
						return errors.New("injected directory sync failure")
					}
					return syncDirectory(path)
				},
			}
			if err := manager.replace(candidate, target); err == nil {
				t.Fatal("replacement unexpectedly succeeded")
			}
			assertFileContent(t, target, test.wantTarget)
			if test.wantCandidate {
				assertFileContent(t, candidate, []byte("new"))
			} else if _, err := os.Lstat(candidate); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("candidate still exists or cannot be inspected: %v", err)
			}
			rollbacks, err := filepath.Glob(filepath.Join(root, ".kado-rollback-*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(rollbacks) != 0 {
				t.Fatalf("rollback placeholders were not cleaned: %v", rollbacks)
			}
		})
	}
}

func TestCleanInstallFaultsPreserveVerifiedCandidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		failRename int
		failSync   int
	}{
		{name: "candidate rename", failRename: 1},
		{name: "installed candidate directory sync", failSync: 1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			target := filepath.Join(root, "kado")
			candidate := filepath.Join(root, ".candidate")
			if err := os.WriteFile(candidate, []byte("new"), 0o755); err != nil {
				t.Fatal(err)
			}
			renameCalls, syncCalls := 0, 0
			manager := Manager{
				rename: func(old, new string) error {
					renameCalls++
					if renameCalls == test.failRename {
						return errors.New("injected rename failure")
					}
					return os.Rename(old, new)
				},
				syncDir: func(path string) error {
					syncCalls++
					if syncCalls == test.failSync {
						return errors.New("injected directory sync failure")
					}
					return syncDirectory(path)
				},
			}
			if err := manager.replace(candidate, target); err == nil {
				t.Fatal("installation unexpectedly succeeded")
			}
			if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("failed installation left a target: %v", err)
			}
			assertFileContent(t, candidate, []byte("new"))
		})
	}
}

func TestUpdateLockPreventsConcurrentReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "kado")
	candidate := filepath.Join(root, ".candidate")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target+".update.lock", []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Manager{}).replace(candidate, target); err == nil {
		t.Fatal("concurrent replacement unexpectedly succeeded")
	}
	assertFileContent(t, target, []byte("old"))
	assertFileContent(t, candidate, []byte("new"))
}

func TestReplacementRejectsAChangedInstalledBinary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "kado")
	candidate := filepath.Join(root, ".candidate")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	expected, err := snapshotExecutable(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("concurrent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (Manager{}).replaceExpected(candidate, target, expected); err == nil {
		t.Fatal("replacement accepted a changed installed binary")
	}
	assertFileContent(t, target, []byte("concurrent"))
	assertFileContent(t, candidate, []byte("new"))
}

func TestVerifyMetadataRequiresCanonicalSignedIdentity(t *testing.T) {
	t.Parallel()

	fixture := newReleaseFixture(t, "0.2.0", runtime.GOOS, runtime.GOARCH, false)
	metadata, err := VerifyMetadata(
		fixture.fetch[fixture.metadataURL],
		fixture.fetch[fixture.metadataURL+".sig"],
		fixture.publicKey,
	)
	if err != nil || metadata.Version != "0.2.0" {
		t.Fatalf("VerifyMetadata result=%#v error=%v", metadata, err)
	}
	pretty, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(fixture.privateKey, pretty)
	if _, err := VerifyMetadata(pretty, signature, fixture.publicKey); !errors.Is(err, errInvalidMetadata) {
		t.Fatalf("noncanonical metadata error = %v", err)
	}
}

func TestVerifyMetadataRejectsInBandSigningKeyRotation(t *testing.T) {
	t.Parallel()

	fixture := newReleaseFixture(t, "0.2.0", runtime.GOOS, runtime.GOARCH, false)
	var metadata Metadata
	if err := json.Unmarshal(fixture.fetch[fixture.metadataURL], &metadata); err != nil {
		t.Fatal(err)
	}
	replacementPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	replacementKeyID, err := KeyID(replacementPublic)
	if err != nil {
		t.Fatal(err)
	}
	metadata.SigningPublicKey = PublicKeyText(replacementPublic)
	metadata.KeyID = replacementKeyID
	encoded, err := CanonicalMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(fixture.privateKey, encoded)
	if _, err := VerifyMetadata(
		encoded,
		signature,
		fixture.publicKey,
	); !errors.Is(err, errInvalidSignature) {
		t.Fatalf("in-band key rotation error = %v", err)
	}
}

func TestVersionPolicyUsesSemverPrereleaseOrdering(t *testing.T) {
	t.Parallel()

	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
	}
	for index := 1; index < len(ordered); index++ {
		comparison, err := compareVersions(ordered[index-1], ordered[index])
		if err != nil || comparison >= 0 {
			t.Fatalf(
				"compareVersions(%q, %q) = %d, %v",
				ordered[index-1],
				ordered[index],
				comparison,
				err,
			)
		}
	}
}

func TestExecutableProvenanceVerificationUsesStampedReleaseIdentity(t *testing.T) {
	t.Parallel()

	fixture := newReleaseFixture(t, "0.2.0", runtime.GOOS, runtime.GOARCH, false)
	var metadata Metadata
	if err := json.Unmarshal(fixture.fetch[fixture.metadataURL], &metadata); err != nil {
		t.Fatal(err)
	}
	output := buildStampedExecutable(t, metadata)
	if err := VerifyExecutable(
		context.Background(),
		output,
		metadata,
		fixture.target,
	); err != nil {
		t.Fatalf("VerifyExecutable error = %v", err)
	}
	metadata.Version = "0.2.1"
	if err := VerifyExecutable(
		context.Background(),
		output,
		metadata,
		fixture.target,
	); !errors.Is(err, ErrCandidate) {
		t.Fatalf("mismatched VerifyExecutable error = %v", err)
	}
	metadata.Version = "0.2.0"
	replacementPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	metadata.SigningPublicKey = PublicKeyText(replacementPublic)
	metadata.KeyID, err = KeyID(replacementPublic)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyExecutable(
		context.Background(),
		output,
		metadata,
		fixture.target,
	); !errors.Is(err, ErrCandidate) {
		t.Fatalf("rotated-key VerifyExecutable error = %v", err)
	}
}

func TestNativeSignedUpdateSmoke(t *testing.T) {
	if os.Getenv("KADO_NATIVE_RELEASE_SMOKE") != "1" {
		t.Skip("set KADO_NATIVE_RELEASE_SMOKE=1 for the native release smoke")
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := KeyID(public)
	if err != nil {
		t.Fatal(err)
	}
	metadataFor := func(version string) Metadata {
		return Metadata{
			Version:          version,
			Commit:           "0123456789abcdef0123456789abcdef01234567",
			BuiltAt:          "2026-07-24T00:00:00Z",
			KeyID:            keyID,
			SigningPublicKey: PublicKeyText(public),
		}
	}
	initial := buildStampedExecutable(t, metadataFor("0.1.0"))
	replacement := buildStampedExecutable(t, metadataFor("0.2.0"))
	replacementBytes, err := os.ReadFile(replacement)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newReleaseFixtureWithBinary(
		t,
		"0.2.0",
		runtime.GOOS,
		runtime.GOARCH,
		false,
		private,
		replacementBytes,
	)
	root := t.TempDir()
	target := filepath.Join(root, executableName(runtime.GOOS))
	initialBytes, err := os.ReadFile(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, initialBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := Manager{
		MetadataURL: fixture.metadataURL,
		PublicKey:   fixture.publicKey,
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		Fetcher:     mapFetcher(fixture.fetch),
	}
	result, err := manager.Update(context.Background(), Options{
		TargetPath:     target,
		CurrentVersion: "0.1.0",
	})
	if err != nil || !result.Changed || result.ToVersion != "0.2.0" {
		t.Fatalf("native update result=%#v error=%v", result, err)
	}
	output, err := exec.Command(target, "version", "--json").Output()
	if err != nil {
		t.Fatalf("updated executable failed: %v", err)
	}
	var version struct {
		Version      string `json:"version"`
		Target       string `json:"target"`
		ReleaseKeyID string `json:"release_key_id"`
	}
	if err := json.Unmarshal(output, &version); err != nil {
		t.Fatal(err)
	}
	if version.Version != "0.2.0" ||
		version.Target != runtime.GOOS+"/"+runtime.GOARCH ||
		version.ReleaseKeyID != keyID {
		t.Fatalf("updated executable identity = %#v", version)
	}
}

type releaseFixture struct {
	metadataURL string
	publicKey   string
	privateKey  ed25519.PrivateKey
	fetch       map[string][]byte
	target      Target
	binary      []byte
	goos        string
	goarch      string
}

func newReleaseFixture(
	t *testing.T,
	version string,
	selectedOS string,
	selectedArch string,
	invalidProvenance bool,
) *releaseFixture {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return newReleaseFixtureWithBinary(
		t,
		version,
		selectedOS,
		selectedArch,
		invalidProvenance,
		private,
		nil,
	)
}

func newReleaseFixtureWithBinary(
	t *testing.T,
	version string,
	selectedOS string,
	selectedArch string,
	invalidProvenance bool,
	private ed25519.PrivateKey,
	selectedBinary []byte,
) *releaseFixture {
	t.Helper()
	public := private.Public().(ed25519.PublicKey)
	keyID, err := KeyID(public)
	if err != nil {
		t.Fatal(err)
	}
	base := "https://kado.so/install/releases/" + version + "/"
	fetch := make(map[string][]byte)
	file := func(name string, value []byte) File {
		url := base + name
		fetch[url] = append([]byte(nil), value...)
		return File{
			Name:   name,
			URL:    url,
			SHA256: Digest(value),
			Size:   int64(len(value)),
		}
	}
	binary := []byte("candidate-" + version + "-" + selectedOS + "-" + selectedArch)
	targets := make([]Target, 0, len(supported))
	var selected Target
	keys := make([]string, 0, len(supported))
	for key := range supported {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := bytes.Split([]byte(key), []byte("/"))
		goos, goarch := string(parts[0]), string(parts[1])
		binaryName := executableName(goos)
		format := "tar.gz"
		archiveSuffix := ".tar.gz"
		if goos == "windows" {
			format = "zip"
			archiveSuffix = ".zip"
		}
		targetBinary := []byte("candidate-" + version + "-" + goos + "-" + goarch)
		if goos == selectedOS && goarch == selectedArch &&
			selectedBinary != nil {
			targetBinary = append([]byte(nil), selectedBinary...)
		}
		archive := validArchive(t, format, binaryName, targetBinary)
		baseName := "kado_" + version + "_" + goos + "_" + goarch
		sbom := sbomBytes(baseName+".spdx.json", version, goos+"/"+goarch)
		target := Target{
			OS:            goos,
			Arch:          goarch,
			BinaryName:    binaryName,
			ArchiveFormat: format,
			Binary:        file(baseName+binarySuffix(goos), targetBinary),
			Archive:       file(baseName+archiveSuffix, archive),
			SBOM:          file(baseName+".spdx.json", sbom),
		}
		if goos == selectedOS && goarch == selectedArch {
			selected = target
			binary = targetBinary
		}
		targets = append(targets, target)
	}
	provenance := provenanceBytes(version, selected, invalidProvenance)
	metadata := Metadata{
		SchemaVersion:    SchemaVersion,
		Product:          Product,
		Version:          version,
		Commit:           "0123456789abcdef0123456789abcdef01234567",
		BuiltAt:          "2026-07-24T00:00:00Z",
		Repository:       "https://github.com/kado-so/search",
		InstallURL:       "https://kado.so/install",
		SigningAlgorithm: "Ed25519",
		KeyID:            keyID,
		SigningPublicKey: PublicKeyText(public),
		Targets:          targets,
		Checksums:        file("checksums.txt", []byte("checksums\n")),
		Provenance:       file("provenance.intoto.json", provenance),
		InstallGuide:     file("INSTALL-CLI.md", []byte("guide\n")),
		InstallUnix:      file("install.sh", []byte("install unix\n")),
		InstallPower:     file("install.ps1", []byte("install powershell\n")),
		UninstallUnix:    file("uninstall.sh", []byte("uninstall unix\n")),
		UninstallPower:   file("uninstall.ps1", []byte("uninstall powershell\n")),
	}
	metadataBytes, err := CanonicalMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	metadataURL := "https://kado.so/install/releases/stable/release-metadata.json"
	fetch[metadataURL] = metadataBytes
	fetch[metadataURL+".sig"] = ed25519.Sign(private, metadataBytes)
	return &releaseFixture{
		metadataURL: metadataURL,
		publicKey:   PublicKeyText(public),
		privateKey:  private,
		fetch:       fetch,
		target:      selected,
		binary:      binary,
		goos:        selectedOS,
		goarch:      selectedArch,
	}
}

func buildStampedExecutable(t *testing.T, metadata Metadata) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), executableName(runtime.GOOS))
	ldflags := "-s -w -buildid=" +
		" -X github.com/kado-so/search/internal/buildinfo.Version=" + metadata.Version +
		" -X github.com/kado-so/search/internal/buildinfo.Commit=" + metadata.Commit +
		" -X github.com/kado-so/search/internal/buildinfo.Date=" + metadata.BuiltAt +
		" -X github.com/kado-so/search/internal/buildinfo.Target=" + runtime.GOOS + "/" + runtime.GOARCH +
		" -X github.com/kado-so/search/internal/buildinfo.ReleaseKeyID=" + metadata.KeyID +
		" -X github.com/kado-so/search/internal/buildinfo.ReleasePublicKey=" + metadata.SigningPublicKey
	command := exec.Command(
		"go",
		"build",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags",
		ldflags,
		"-o",
		output,
		"./cmd/kado",
	)
	command.Dir = filepath.Join("..", "..")
	if value, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go build error = %v\n%s", err, value)
	}
	return output
}

func (fixture *releaseFixture) manager(t *testing.T) Manager {
	t.Helper()
	return Manager{
		MetadataURL: fixture.metadataURL,
		PublicKey:   fixture.publicKey,
		GOOS:        fixture.goos,
		GOARCH:      fixture.goarch,
		Fetcher:     mapFetcher(fixture.fetch),
		VerifyCandidate: func(
			_ context.Context,
			path string,
			_ Metadata,
			_ Target,
		) error {
			value, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(value, fixture.binary) {
				return ErrCandidate
			}
			return nil
		},
	}
}

type mapFetcher map[string][]byte

func (fetcher mapFetcher) Fetch(
	_ context.Context,
	url string,
	limit int64,
) ([]byte, error) {
	value, ok := fetcher[url]
	if !ok || int64(len(value)) > limit {
		return nil, errors.New("missing fixture")
	}
	return append([]byte(nil), value...), nil
}

func resignInvalidProvenance(fixture *releaseFixture) *releaseFixture {
	var metadata Metadata
	_ = json.Unmarshal(fixture.fetch[fixture.metadataURL], &metadata)
	invalid := provenanceBytes(metadata.Version, fixture.target, true)
	metadata.Provenance.SHA256 = Digest(invalid)
	metadata.Provenance.Size = int64(len(invalid))
	fixture.fetch[metadata.Provenance.URL] = invalid
	encoded, _ := CanonicalMetadata(metadata)
	fixture.fetch[fixture.metadataURL] = encoded
	fixture.fetch[fixture.metadataURL+".sig"] = ed25519.Sign(fixture.privateKey, encoded)
	return fixture
}

func provenanceBytes(version string, target Target, invalid bool) []byte {
	archiveDigest := target.Archive.SHA256
	if invalid {
		archiveDigest = Digest([]byte("wrong provenance"))
	}
	value := map[string]any{
		"_type":         "https://in-toto.io/Statement/v1",
		"predicateType": "https://slsa.dev/provenance/v1",
		"subject": []any{
			map[string]any{"name": target.Binary.Name, "digest": map[string]string{"sha256": target.Binary.SHA256}},
			map[string]any{"name": target.Archive.Name, "digest": map[string]string{"sha256": archiveDigest}},
			map[string]any{"name": target.SBOM.Name, "digest": map[string]string{"sha256": target.SBOM.SHA256}},
		},
		"predicate": map[string]any{
			"buildDefinition": map[string]any{
				"buildType": "https://kado.so/build-types/go-cli-release/v1",
				"externalParameters": map[string]string{
					"version": version,
					"commit":  "0123456789abcdef0123456789abcdef01234567",
				},
				"internalParameters": map[string]any{},
				"resolvedDependencies": []any{
					map[string]any{
						"uri": "git+https://github.com/kado-so/search@0123456789abcdef0123456789abcdef01234567",
						"digest": map[string]string{
							"gitCommit": "0123456789abcdef0123456789abcdef01234567",
						},
					},
				},
			},
			"runDetails": map[string]any{
				"builder": map[string]string{
					"id": "https://github.com/kado-so/search/tree/main/tools/release",
				},
				"metadata": map[string]string{
					"invocationId": version + "@0123456789abcdef0123456789abcdef01234567",
					"startedOn":    "2026-07-24T00:00:00Z",
					"finishedOn":   "2026-07-24T00:00:00Z",
				},
				"byproducts": []any{},
			},
		},
	}
	encoded, _ := json.Marshal(value)
	return append(encoded, '\n')
}

func sbomBytes(name, version, target string) []byte {
	value := map[string]any{
		"spdxVersion": "SPDX-2.3",
		"name":        name,
		"comment":     target,
		"packages": []any{
			map[string]string{"name": Product, "versionInfo": version},
		},
	}
	encoded, _ := json.Marshal(value)
	return append(encoded, '\n')
}

func validArchive(t *testing.T, format, binaryName string, binary []byte) []byte {
	t.Helper()
	if format == "zip" {
		return makeTestZip(t, []zipEntry{
			{name: binaryName, mode: 0o755, value: binary},
			{name: "LICENSE", mode: 0o644, value: []byte("license")},
			{name: "INSTALL-CLI.md", mode: 0o644, value: []byte("guide")},
		})
	}
	return makeTestTar(t, []tarEntry{
		{name: binaryName, mode: 0o755, value: binary},
		{name: "LICENSE", mode: 0o644, value: []byte("license")},
		{name: "INSTALL-CLI.md", mode: 0o644, value: []byte("guide")},
	})
}

type tarEntry struct {
	name     string
	mode     int64
	typeflag byte
	value    []byte
}

func makeTestTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Typeflag: typeflag,
			Size:     int64(len(entry.value)),
		}
		if typeflag == tar.TypeSymlink {
			header.Linkname = "other"
			header.Size = 0
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := archive.Write(entry.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

type zipEntry struct {
	name  string
	mode  fs.FileMode
	value []byte
}

func makeTestZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetMode(entry.mode)
		header.SetModTime(time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC))
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func executableName(goos string) string {
	if goos == "windows" {
		return "kado.exe"
	}
	return "kado"
}

func binarySuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", filepath.Base(path), err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadFile(%s) = %q, want %q", filepath.Base(path), got, want)
	}
}
