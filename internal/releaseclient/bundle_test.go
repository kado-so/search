package releaseclient

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io/fs"
	"strings"
	"testing"
	"time"
)

type bundleTestEntry struct {
	name string
	mode fs.FileMode
	data []byte
	kind byte
}

func TestExtractBundleAcceptsExactFiveEntryContract(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"tar.gz", "zip"} {
		format := format
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			binaryName := "kado"
			if format == "zip" {
				binaryName = "kado.exe"
			}
			archive := makeBundleTestArchive(t, format, validBundleEntries(binaryName))
			got, err := ExtractBundle(archive, format, binaryName)
			if err != nil {
				t.Fatalf("ExtractBundle() error = %v", err)
			}
			if string(got.Kado) != "kado-binary" || string(got.A2A) != "a2a-binary" {
				t.Fatalf("ExtractBundle() = %#v", got)
			}
		})
	}
}

func TestExtractBundleRejectsMalformedContracts(t *testing.T) {
	t.Parallel()
	large := bytes.Repeat([]byte{'x'}, maxArchiveSupportSize+1)
	for _, format := range []string{"tar.gz", "zip"} {
		format := format
		binaryName := "kado"
		if format == "zip" {
			binaryName = "kado.exe"
		}
		base := validBundleEntries(binaryName)
		cases := map[string][]bundleTestEntry{
			"missing":    append([]bundleTestEntry(nil), base[:4]...),
			"additional": append(append([]bundleTestEntry(nil), base...), bundleTestEntry{name: "extra", mode: 0o644, data: []byte("x")}),
			"duplicate":  append(append([]bundleTestEntry(nil), base...), base[0]),
			"unsafe":     append(append([]bundleTestEntry(nil), base...), bundleTestEntry{name: "../escape", mode: 0o644, data: []byte("x")}),
			"wrong-mode": replaceBundleEntry(base, 1, bundleTestEntry{name: a2aNameForTest(binaryName), mode: 0o644, data: []byte("a2a-binary")}),
			"oversized":  replaceBundleEntry(base, 2, bundleTestEntry{name: "LICENSE", mode: 0o644, data: large}),
			"unsafe-type": replaceBundleEntry(base, 2, bundleTestEntry{
				name: "LICENSE", mode: fs.ModeSymlink | 0o777, data: []byte("target"), kind: tar.TypeSymlink,
			}),
		}
		for name, entries := range cases {
			name, entries := name, entries
			t.Run(format+"/"+name, func(t *testing.T) {
				t.Parallel()
				archive := makeBundleTestArchive(t, format, entries)
				if _, err := ExtractBundle(archive, format, binaryName); err == nil {
					t.Fatal("ExtractBundle() accepted malformed archive")
				}
			})
		}
	}
}

func TestVerifyTargetBundleAuthenticatesArchiveAndSidecar(t *testing.T) {
	t.Parallel()

	archive := makeBundleTestArchive(t, "zip", validBundleEntries("kado.exe"))
	target := Target{
		OS: "windows", Arch: "amd64",
		Archive: File{SHA256: Digest(archive), Size: int64(len(archive))},
		Sidecar: EmbeddedArtifact{SHA256: Digest([]byte("a2a-binary")), Size: int64(len("a2a-binary"))},
	}
	if _, err := VerifyTargetBundle(target, archive); err != nil {
		t.Fatalf("VerifyTargetBundle() error = %v", err)
	}

	tamperedArchive := append([]byte(nil), archive...)
	tamperedArchive[len(tamperedArchive)/2] ^= 1
	if _, err := VerifyTargetBundle(target, tamperedArchive); err == nil {
		t.Fatal("VerifyTargetBundle() accepted a changed archive")
	}
	tamperedSidecar := target
	tamperedSidecar.Sidecar.SHA256 = strings.Repeat("0", 64)
	if _, err := VerifyTargetBundle(tamperedSidecar, archive); err == nil {
		t.Fatal("VerifyTargetBundle() accepted a mismatched sidecar digest")
	}
	tamperedSidecar = target
	tamperedSidecar.Sidecar.Size++
	if _, err := VerifyTargetBundle(tamperedSidecar, archive); err == nil {
		t.Fatal("VerifyTargetBundle() accepted a mismatched sidecar size")
	}
}

func validBundleEntries(binaryName string) []bundleTestEntry {
	return []bundleTestEntry{
		{name: binaryName, mode: 0o755, data: []byte("kado-binary")},
		{name: a2aNameForTest(binaryName), mode: 0o755, data: []byte("a2a-binary")},
		{name: "LICENSE", mode: 0o644, data: []byte("MIT")},
		{name: a2aLicenseName, mode: 0o644, data: []byte("Apache-2.0")},
		{name: "INSTALL-CLI.md", mode: 0o644, data: []byte("install")},
	}
}

func replaceBundleEntry(source []bundleTestEntry, index int, replacement bundleTestEntry) []bundleTestEntry {
	result := append([]bundleTestEntry(nil), source...)
	result[index] = replacement
	return result
}

func a2aNameForTest(binaryName string) string {
	name, _ := a2aExecutableName(binaryName)
	return name
}

func makeBundleTestArchive(t *testing.T, format string, entries []bundleTestEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	if format == "tar.gz" {
		compressed := gzip.NewWriter(&output)
		archive := tar.NewWriter(compressed)
		for _, entry := range entries {
			kind := entry.kind
			if kind == 0 {
				kind = tar.TypeReg
			}
			header := &tar.Header{Name: entry.name, Mode: int64(entry.mode.Perm()), Typeflag: kind}
			if kind == tar.TypeReg {
				header.Size = int64(len(entry.data))
			}
			if err := archive.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if kind == tar.TypeReg {
				if _, err := archive.Write(entry.data); err != nil {
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
	archive := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetModTime(time.Unix(315532800, 0).UTC())
		header.SetMode(entry.mode)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
