package buildinfo

import (
	"strings"
	"testing"
)

func TestLineIsSingleLineAndBounded(t *testing.T) {
	t.Parallel()

	info := Info{
		Version: strings.Repeat("v", 100) + "\nsecret",
		Commit:  "abc123\rsecond-line",
		Date:    "",
	}
	line := info.Line()

	if strings.ContainsAny(line, "\r\n") {
		t.Fatalf("version line contains a line break: %q", line)
	}
	if len(line) > 180 {
		t.Fatalf("version line is not bounded: %d bytes", len(line))
	}
	if !strings.HasPrefix(line, "kado ") {
		t.Fatalf("version line has unexpected prefix: %q", line)
	}
}

func TestCurrentUsesBuildVariables(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	originalTarget, originalKeyID := Target, ReleaseKeyID
	originalPublicKey, originalMetadataURL := ReleasePublicKey, ReleaseMetadataURL
	originalInstallChannel := InstallChannel
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
		Target, ReleaseKeyID = originalTarget, originalKeyID
		ReleasePublicKey, ReleaseMetadataURL = originalPublicKey, originalMetadataURL
		InstallChannel = originalInstallChannel
	})

	Version, Commit, Date = "v1.2.3", "abc123", "2026-07-23T00:00:00Z"
	Target, ReleaseKeyID = "linux/amd64", "sha256:key"
	ReleasePublicKey = "public"
	ReleaseMetadataURL = "https://kado.so/install/releases/stable/release-metadata.json"
	InstallChannel = "direct"

	got := Current()
	if got.Version != Version || got.Commit != Commit || got.Date != Date ||
		got.Target != Target || got.ReleaseKeyID != ReleaseKeyID ||
		got.ReleasePublicKey != ReleasePublicKey ||
		got.ReleaseMetadataURL != ReleaseMetadataURL {
		t.Fatalf("Current() = %#v", got)
	}
	if got.InstallChannel != "direct" {
		t.Fatalf("Current().InstallChannel = %q", got.InstallChannel)
	}
}

func TestJSONIsDeterministicNonSecretProvenance(t *testing.T) {
	t.Parallel()

	info := Info{
		Version:            "0.1.0",
		Commit:             "0123456789abcdef",
		Date:               "2026-07-24T00:00:00Z",
		Target:             "darwin/arm64",
		ReleaseKeyID:       "sha256:abc",
		ReleasePublicKey:   "must-not-appear",
		ReleaseMetadataURL: "https://example.test/private-path",
	}
	got, err := info.JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	want := "{\"version\":\"0.1.0\",\"commit\":\"0123456789abcdef\",\"built_at\":\"2026-07-24T00:00:00Z\",\"target\":\"darwin/arm64\",\"release_key_id\":\"sha256:abc\",\"release_public_key\":\"must-not-appear\"}\n"
	if string(got) != want {
		t.Fatalf("JSON() = %q, want %q", got, want)
	}
	if strings.Contains(string(got), "private-path") {
		t.Fatalf("JSON() exposed release endpoint configuration: %q", got)
	}
}
