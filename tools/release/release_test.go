package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/kado-so/search/internal/releaseclient"
	"github.com/kado-so/search/internal/skillclient"
)

func TestDeterministicArchivesHaveSafePathsAndModes(t *testing.T) {
	t.Parallel()

	builtAt := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	binary := []byte("test executable")
	license := []byte("license\n")
	guide := []byte("guide\n")

	firstTar, err := makeTarGzip(builtAt, "kado", binary, license, guide)
	if err != nil {
		t.Fatal(err)
	}
	secondTar, err := makeTarGzip(builtAt, "kado", binary, license, guide)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstTar, secondTar) {
		t.Fatal("tar.gz output is not deterministic")
	}
	extracted, err := releaseclient.ExtractBinary(firstTar, "tar.gz", "kado")
	if err != nil || !bytes.Equal(extracted, binary) {
		t.Fatalf("ExtractBinary(tar.gz) = %q, %v", extracted, err)
	}

	firstZip, err := makeZip(builtAt, "kado.exe", binary, license, guide)
	if err != nil {
		t.Fatal(err)
	}
	secondZip, err := makeZip(builtAt, "kado.exe", binary, license, guide)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstZip, secondZip) {
		t.Fatal("zip output is not deterministic")
	}
	extracted, err = releaseclient.ExtractBinary(firstZip, "zip", "kado.exe")
	if err != nil || !bytes.Equal(extracted, binary) {
		t.Fatalf("ExtractBinary(zip) = %q, %v", extracted, err)
	}
}

func TestSkillReleaseIsDeterministicAndSelfVerifying(t *testing.T) {
	t.Parallel()

	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	private := ed25519.NewKeyFromSeed(seed)
	builtAt := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	firstArchive, firstMetadata, firstSignature, err := makeSkillRelease(
		builtAt,
		"https://kado.so/install",
		"0.1.0",
		private,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondArchive, secondMetadata, secondSignature, err := makeSkillRelease(
		builtAt,
		"https://kado.so/install",
		"0.1.0",
		private,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstArchive, secondArchive) ||
		!bytes.Equal(firstMetadata, secondMetadata) ||
		!bytes.Equal(firstSignature, secondSignature) {
		t.Fatal("skill release output is not deterministic")
	}
	files, err := skillclient.ExtractArchive(firstArchive)
	if err != nil || len(files["SKILL.md"]) == 0 {
		t.Fatalf("ExtractArchive() = %#v, %v", files, err)
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
	for _, install := range documents[:3] {
		if !strings.Contains(install, "credentials") ||
			!strings.Contains(install, "identity") {
			t.Fatalf("install description lost security policy: %q", install)
		}
	}
	for _, uninstall := range documents[3:] {
		if !strings.Contains(strings.ToLower(uninstall), "purge") ||
			!strings.Contains(strings.ToLower(uninstall), "preserv") {
			t.Fatalf("uninstall description lost credential policy: %q", uninstall)
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
