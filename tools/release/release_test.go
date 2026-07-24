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

func TestGeneratedInstallAndUninstallDocumentsEnforcePolicy(t *testing.T) {
	t.Parallel()

	var source distributionSource
	source.Plugin.Version = "0.1.0"
	source.Plugin.Repository = "https://github.com/kado-so/search"
	source.Installation.CLIInstallURL = "https://kado.so/install"
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
			"curl ",
			"wget ",
			"Invoke-WebRequest",
			"PRIVATE KEY",
			"KADO_RELEASE_SIGNING_KEY",
		} {
			if strings.Contains(document, forbidden) {
				t.Fatalf("generated document contains %q", forbidden)
			}
		}
	}
	for _, install := range documents[:3] {
		if !strings.Contains(install, "credentials") ||
			!strings.Contains(install, "provenance") {
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
