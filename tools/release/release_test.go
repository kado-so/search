package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kado-so/search/internal/releaseclient"
	"github.com/kado-so/search/internal/skillclient"
)

func TestArchivesHaveSafePathsAndModes(t *testing.T) {
	t.Parallel()

	builtAt := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	binary := []byte("test executable")
	a2aBinary := []byte("test A2A executable")
	license := []byte("license\n")
	a2aLicense := []byte("A2A license\n")
	guide := []byte("guide\n")

	tarArchive, err := makeTarGzip(builtAt, "kado", binary, a2aBinary, license, a2aLicense, guide)
	if err != nil {
		t.Fatal(err)
	}
	extracted, err := releaseclient.ExtractBundle(tarArchive, "tar.gz", "kado")
	if err != nil || !bytes.Equal(extracted.Kado, binary) || !bytes.Equal(extracted.A2A, a2aBinary) {
		t.Fatalf("ExtractBundle(tar.gz) = %#v, %v", extracted, err)
	}

	zipArchive, err := makeZip(builtAt, "kado.exe", binary, a2aBinary, license, a2aLicense, guide)
	if err != nil {
		t.Fatal(err)
	}
	extracted, err = releaseclient.ExtractBundle(zipArchive, "zip", "kado.exe")
	if err != nil || !bytes.Equal(extracted.Kado, binary) || !bytes.Equal(extracted.A2A, a2aBinary) {
		t.Fatalf("ExtractBundle(zip) = %#v, %v", extracted, err)
	}
}

func TestSkillReleaseIsSelfVerifying(t *testing.T) {
	t.Parallel()

	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	private := ed25519.NewKeyFromSeed(seed)
	archive, _, _, err := makeSkillRelease(
		"https://kado.so/install",
		private,
	)
	if err != nil {
		t.Fatal(err)
	}
	files, err := skillclient.ExtractArchive(archive)
	if err != nil || len(files["SKILL.md"]) == 0 {
		t.Fatalf("ExtractArchive() = %#v, %v", files, err)
	}
}

func TestEveryEmbeddedSkillHasSignedRemoteRelease(t *testing.T) {
	t.Parallel()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	set, err := makeSkillReleases("https://kado.so/install", private)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Releases) != 4 {
		t.Fatalf("makeSkillReleases() emitted %d releases", len(set.Releases))
	}
	seen := map[string]bool{}
	for _, release := range set.Releases {
		files, err := skillclient.ExtractArchive(release.Archive, release.Name)
		if err != nil || len(files["SKILL.md"]) == 0 {
			t.Fatalf("%s/%s archive: %v", release.Name, release.Variant, err)
		}
		seen[release.Name+":"+release.Variant] = true
	}
	if !seen["kado-mcp:default"] || !seen["kado-a2a:default"] || !seen["kado-cli-non-search:default"] || !seen["kado-search:default"] {
		t.Fatalf("missing releases: %#v", seen)
	}
}

func TestReleaseBuildWritesNestedSkillArtifacts(t *testing.T) {
	t.Parallel()

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	files := map[string]releaseclient.File{}
	add := func(name string, value []byte, mode fs.FileMode) (releaseclient.File, error) {
		if err := writeReleaseArtifact(output, name, value, mode); err != nil {
			return releaseclient.File{}, err
		}
		file := releaseclient.File{Name: name, Size: int64(len(value)), SHA256: releaseclient.Digest(value)}
		files[name] = file
		return file, nil
	}
	if err := addSkillReleaseArtifacts("https://kado.so/install", private, add); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"skills/kado-mcp/default/0.1.0/kado-mcp.tar.gz",
		"skills/catalog.json",
		"skills/catalog.json.sig",
		"skills/kado-a2a/default/0.1.0/kado-a2a.tar.gz",
		"skills/kado-a2a/default/0.1.0/metadata.json",
		"skills/kado-a2a/default/0.1.0/metadata.json.sig",
		"skills/kado-cli-non-search/default/0.2.0/kado-cli-non-search.tar.gz",
		"skills/kado-search/default/0.4.0/kado-search.tar.gz",
	} {
		if _, ok := files[name]; !ok {
			t.Fatalf("skill artifact %q was not registered", name)
		}
		if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(name))); err != nil {
			t.Fatalf("skill artifact %q was not written: %v", name, err)
		}
	}
	if len(files) != 14 {
		t.Fatalf("skill artifact count = %d, want 14", len(files))
	}
}

func TestSkillReleaseIsIndependentOfCLIBuild(t *testing.T) {
	t.Parallel()

	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	private := ed25519.NewKeyFromSeed(seed)
	firstArchive, firstMetadata, firstSignature, err := makeSkillRelease(
		"https://kado.so/install",
		private,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondArchive, secondMetadata, secondSignature, err := makeSkillRelease(
		"https://kado.so/install",
		private,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstArchive, secondArchive) ||
		!bytes.Equal(firstMetadata, secondMetadata) ||
		!bytes.Equal(firstSignature, secondSignature) {
		t.Fatal("unchanged skill release is not reproducible")
	}
}

func TestReleaseIdentityRequiresExplicitSemanticVersion(t *testing.T) {
	t.Parallel()

	identity, err := newReleaseIdentity("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Version != "1.2.3" ||
		identity.Repository != releaseRepository ||
		identity.InstallURL != releaseInstallURL ||
		identity.Executable != releaseExecutable {
		t.Fatalf("release identity = %#v", identity)
	}
	for _, invalid := range []string{"", "v1.2.3", "01.2.3", "1.2"} {
		if _, err := newReleaseIdentity(invalid); err == nil {
			t.Fatalf("newReleaseIdentity(%q) succeeded", invalid)
		}
	}
}

func TestPinnedGoVersionComesFromGoModToolchain(t *testing.T) {
	t.Parallel()

	version, err := pinnedGoVersion([]byte(
		"module github.com/kado-so/search\n\ngo 1.26.0\n\ntoolchain go1.26.4\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if version != "go1.26.4" {
		t.Fatalf("pinnedGoVersion() = %q", version)
	}
	crlfVersion, err := pinnedGoVersion([]byte(
		"module github.com/kado-so/search\r\n\r\ngo 1.26.0\r\n\r\ntoolchain go1.26.4\r\n",
	))
	if err != nil || crlfVersion != "go1.26.4" {
		t.Fatalf("pinnedGoVersion(CRLF) = %q, %v", crlfVersion, err)
	}
	for _, invalid := range [][]byte{
		[]byte("module github.com/kado-so/search\ngo 1.26.0\n"),
		[]byte("toolchain default\n"),
		[]byte("toolchain go1.26\n"),
	} {
		if _, err := pinnedGoVersion(invalid); err == nil {
			t.Fatalf("pinnedGoVersion(%q) succeeded", invalid)
		}
	}
}

func TestRunRejectsUnknownInstallChannelBeforeEnvironmentAccess(t *testing.T) {
	t.Parallel()

	err := run(options{
		root:    t.TempDir(),
		output:  t.TempDir(),
		commit:  strings.Repeat("a", 40),
		epoch:   315532800,
		version: "1.2.3",
		channel: "brew",
	})
	if err == nil || err.Error() != "--install-channel is invalid" {
		t.Fatalf("run() error = %v", err)
	}
}

func TestGeneratedInstallAndUninstallDocumentsEnforcePolicy(t *testing.T) {
	t.Parallel()

	source := releaseIdentity{
		Version:    "0.1.0",
		Repository: "https://github.com/kado-so/search",
		InstallURL: "https://kado.so/install",
		Executable: "kado",
	}
	keyID := "sha256:0123456789abcdef"
	documents := []string{
		installGuide(source, keyID),
		installUnixScript(source, keyID),
		installPowerShellScript(source, keyID),
		uninstallUnixScript(),
		uninstallPowerShellScript(),
	}
	for _, document := range documents {
		for _, forbidden := range []string{
			"PRIVATE KEY",
			"KADO_RELEASE_SIGNING_KEY",
		} {
			if strings.Contains(document, forbidden) {
				t.Fatalf("generated document contains %q", forbidden)
			}
		}
	}
	if !strings.Contains(documents[1], "command -v curl") ||
		!strings.Contains(documents[1], "command -v wget") ||
		!strings.Contains(documents[2], "Invoke-WebRequest") {
		t.Fatal("generated installers do not provide direct HTTPS bootstrap")
	}
	for _, install := range documents[1:3] {
		if !strings.Contains(install, " update") ||
			!strings.Contains(install, "skill install") ||
			!strings.Contains(install, "auth create") ||
			!strings.Contains(install, "auth status") ||
			!strings.Contains(install, "skills synchronized and authentication verified") {
			t.Fatalf("generated installer does not update and finish setup: %q", install)
		}
	}
	for _, install := range documents[:3] {
		if !strings.Contains(strings.ToLower(install), "authenticat") ||
			!strings.Contains(install, "identity") {
			t.Fatalf("install description lost security policy: %q", install)
		}
	}
	for _, uninstall := range documents[3:] {
		if !strings.Contains(strings.ToLower(uninstall), "purge") ||
			!strings.Contains(strings.ToLower(uninstall), "preserv") ||
			!strings.Contains(uninstall, "uninstall --yes") {
			t.Fatalf("uninstall description lost credential policy: %q", uninstall)
		}
	}
	for _, install := range documents[1:3] {
		if !strings.Contains(install, "__install-bundle") || strings.Contains(install, "Expand-Archive") || strings.Contains(install, "tar -xzf") {
			t.Fatal("bootstrap must delegate bounded signed extraction to Kado")
		}
	}

}

func TestGeneratedUnixInstallerUpdatesThenFinishesSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell installer test")
	}

	root := t.TempDir()
	installDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "commands.log")
	executable := filepath.Join(installDir, "kado")
	fake := "#!/bin/sh\nprintf '%s:%s\\n' \"${KADO_INSTALL_COHORT:-}\" \"$*\" >>\"$KADO_TEST_LOG\"\n"
	if err := os.WriteFile(executable, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	script := installUnixScript(releaseIdentity{InstallURL: "https://kado.so/install"}, "unused")
	command := exec.Command("sh", "-c", script)
	command.Env = append(
		os.Environ(),
		"KADO_INSTALL_DIR="+installDir,
		"KADO_INSTALL_COHORT=design_partner",
		"KADO_NO_MODIFY_PATH=1",
		"KADO_TEST_LOG="+logPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("installer failed: %v\n%s", err, output)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "design_partner:update\n" +
		"design_partner:skill install\n" +
		"design_partner:auth create\n" +
		"design_partner:auth status\n"
	if string(logged) != want {
		t.Fatalf("commands = %q, want %q", logged, want)
	}
}

func TestGeneratedUnixInstallerStillAuthenticatesAfterSkillFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell installer test")
	}

	root := t.TempDir()
	installDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "commands.log")
	executable := filepath.Join(installDir, "kado")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$KADO_TEST_LOG\"\n" +
		"test \"$*\" != 'skill install'\n"
	if err := os.WriteFile(executable, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	script := installUnixScript(releaseIdentity{InstallURL: "https://kado.so/install"}, "unused")
	command := exec.Command("sh", "-c", script)
	command.Env = append(
		os.Environ(),
		"KADO_INSTALL_DIR="+installDir,
		"KADO_NO_MODIFY_PATH=1",
		"KADO_TEST_LOG="+logPath,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installer failed: %v\n%s", err, output)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "auth create\nauth status\n") {
		t.Fatalf("authentication did not run after skill failure: %q", logged)
	}
	if strings.Contains(string(output), "skills synchronized") ||
		!strings.Contains(string(output), "skill setup requires retry") {
		t.Fatalf("installer reported incorrect skill status: %q", output)
	}
}

func TestGeneratedScriptsHaveValidNativeSyntax(t *testing.T) {
	root := t.TempDir()
	if shell, err := exec.LookPath("sh"); err == nil {
		for name, value := range map[string]string{
			"install.sh":   installUnixScript(releaseIdentity{InstallURL: "https://kado.so/install"}, "unused"),
			"uninstall.sh": uninstallUnixScript(),
		} {
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(shell, "-n", path).CombinedOutput(); err != nil {
				t.Fatalf("%s syntax: %v\n%s", name, err, output)
			}
		}
	}
	if powershell, err := exec.LookPath("powershell.exe"); runtime.GOOS == "windows" && err == nil {
		for name, value := range map[string]string{
			"install.ps1":   installPowerShellScript(releaseIdentity{InstallURL: "https://kado.so/install"}, "unused"),
			"uninstall.ps1": uninstallPowerShellScript(),
		} {
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
				t.Fatal(err)
			}
			command := `$tokens = $null; $parseErrors = $null; [System.Management.Automation.Language.Parser]::ParseFile($env:KADO_SCRIPT_TO_PARSE, [ref]$tokens, [ref]$parseErrors) | Out-Null; if ($parseErrors.Count -ne 0) { $parseErrors | Out-String | Write-Error; exit 1 }`
			process := exec.Command(powershell, "-NoProfile", "-Command", command)
			process.Env = append(os.Environ(), "KADO_SCRIPT_TO_PARSE="+path)
			if output, err := process.CombinedOutput(); err != nil {
				t.Fatalf("%s syntax: %v\n%s", name, err, output)
			}
		}
	}
}

func TestSigningKeyComesOnlyFromEnvironmentAndErrorsAreSecretSafe(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	secret := base64.StdEncoding.EncodeToString(seed)
	t.Setenv(signingKeyEnvironment, secret)
	private, err := signingKeyFromEnvironment()
	if err != nil || len(private) != ed25519.PrivateKeySize {
		t.Fatalf("signingKeyFromEnvironment error = %v", err)
	}

	t.Setenv(signingKeyEnvironment, secret+"invalid")
	_, err = signingKeyFromEnvironment()
	if err == nil {
		t.Fatal("invalid signing key was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("signing error exposed private seed")
	}
	for _, entry := range sanitizedEnvironment() {
		if strings.HasPrefix(entry, signingKeyEnvironment+"=") {
			t.Fatal("signing seed was inherited by a build subprocess")
		}
	}
}
