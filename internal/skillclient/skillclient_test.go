package skillclient

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/kado-so/search/internal/releaseclient"
)

func TestMetadataSignatureAndSameOrigin(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	metadataURL := "https://kado.so/install/skills/kado-search/latest/metadata.json"
	value := Metadata{
		SchemaVersion:     SchemaVersion,
		Name:              SkillName,
		Version:           "0.2.0",
		MinimumCLIVersion: "0.1.0",
		Archive: Archive{
			URL:    "https://kado.so/install/skills/kado-search/latest/kado-search.tar.gz",
			Size:   123,
			SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	encoded, err := CanonicalMetadata(value)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(private, encoded)
	verified, err := VerifyMetadata(
		encoded,
		signature,
		releaseclient.PublicKeyText(public),
		metadataURL,
	)
	if err != nil || verified.Version != "0.2.0" {
		t.Fatalf("VerifyMetadata() = %#v, %v", verified, err)
	}
	value.Archive.URL = "https://example.com/kado-search.tar.gz"
	encoded, _ = CanonicalMetadata(value)
	signature = ed25519.Sign(private, encoded)
	if _, err := VerifyMetadata(
		encoded,
		signature,
		releaseclient.PublicKeyText(public),
		metadataURL,
	); err == nil {
		t.Fatal("cross-origin skill archive was accepted")
	}
}

func TestExtractArchiveRejectsTraversalAndSymlinks(t *testing.T) {
	t.Parallel()
	valid := makeTestArchive(t, []tar.Header{{
		Name:     "kado-search/SKILL.md",
		Mode:     0o644,
		Size:     5,
		Typeflag: tar.TypeReg,
	}}, [][]byte{[]byte("skill")})
	files, err := ExtractArchive(valid)
	if err != nil || string(files["SKILL.md"]) != "skill" {
		t.Fatalf("ExtractArchive(valid) = %#v, %v", files, err)
	}
	for _, test := range []struct {
		header tar.Header
		value  []byte
	}{
		{header: tar.Header{Name: "kado-search/../escape", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}, value: []byte("x")},
		{header: tar.Header{Name: "kado-search/link", Mode: 0o644, Size: 0, Typeflag: tar.TypeSymlink}},
	} {
		encoded := makeTestArchive(t, []tar.Header{test.header}, [][]byte{test.value})
		if _, err := ExtractArchive(encoded); err == nil {
			t.Fatalf("unsafe header %#v was accepted", test.header)
		}
	}
}

func TestEmbeddedInstallTracksOwnershipAndProtectsModifications(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manager := Manager{
		ConfigDir:      root + "/config",
		HomeDir:        root + "/home",
		CurrentVersion: "dev",
	}
	result, err := manager.Install(context.Background(), InstallOptions{
		CurrentAgent: "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installed) != 2 || !result.UsedFallback {
		t.Fatalf("Install() = %#v", result)
	}
	item := installationForAgent(t, result.Installed, "codex")
	if err := os.WriteFile(filepath.Join(item.Path, "SKILL.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	update, updateErr := manager.Update(context.Background())
	if updateErr != nil || update.Failures[item.Path] != "locally_modified" {
		t.Fatalf("Update() = %#v, %v", update, updateErr)
	}
}

func installationForAgent(t *testing.T, values []Installation, agent string) Installation {
	t.Helper()
	for _, value := range values {
		if value.Agent == agent {
			return value
		}
	}
	t.Fatalf("installation for %q not found in %#v", agent, values)
	return Installation{}
}

func makeTestArchive(
	t *testing.T,
	headers []tar.Header,
	values [][]byte,
) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for index := range headers {
		header := headers[index]
		if err := archive.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if len(values[index]) > 0 {
			_, _ = archive.Write(values[index])
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
