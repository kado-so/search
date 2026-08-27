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
	originalA2AVersion, originalA2ATag := A2AVersion, A2ATag
	originalA2ACommit, originalA2ADate := A2AUpstreamCommit, A2ADate
	originalA2ATarget, originalA2APatchSet := A2ATarget, A2APatchSet
	originalA2ASHA256, originalA2ASize := A2AArtifactSHA256, A2AArtifactSize
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
		Target, ReleaseKeyID = originalTarget, originalKeyID
		ReleasePublicKey, ReleaseMetadataURL = originalPublicKey, originalMetadataURL
		InstallChannel = originalInstallChannel
		A2AVersion, A2ATag = originalA2AVersion, originalA2ATag
		A2AUpstreamCommit, A2ADate = originalA2ACommit, originalA2ADate
		A2ATarget, A2APatchSet = originalA2ATarget, originalA2APatchSet
		A2AArtifactSHA256, A2AArtifactSize = originalA2ASHA256, originalA2ASize
	})

	Version, Commit, Date = "v1.2.3", "abc123", "2026-07-23T00:00:00Z"
	Target, ReleaseKeyID = "linux/amd64", "sha256:key"
	ReleasePublicKey = "public"
	ReleaseMetadataURL = "https://kado.so/install/releases/stable/release-metadata.json"
	InstallChannel = "direct"
	A2AVersion, A2ATag = "0.3.1-dev", "none"
	A2AUpstreamCommit, A2ADate = "def456", "2026-08-27T00:00:00Z"
	A2ATarget, A2APatchSet = "linux/amd64", "sha256:patch"
	A2AArtifactSHA256, A2AArtifactSize = "sha256:artifact", "12345"

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
	if got.A2A.Version != A2AVersion || got.A2A.UpstreamCommit != A2AUpstreamCommit ||
		got.A2A.Target != A2ATarget || got.A2A.ArtifactSize != 12345 {
		t.Fatalf("Current().A2A = %#v", got.A2A)
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
		A2A: A2AInfo{
			Version:        "0.3.1-dev",
			Tag:            "none",
			UpstreamCommit: "fedcba9876543210",
			Date:           "2026-07-24T00:00:00Z",
			Target:         "darwin/arm64",
			PatchSet:       "sha256:patch",
			ArtifactSHA256: "sha256:artifact",
			ArtifactSize:   12345,
		},
	}
	got, err := info.JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	want := "{\"schema_version\":\"kado.version.v1\",\"kado\":{\"version\":\"0.1.0\",\"commit\":\"0123456789abcdef\",\"built_at\":\"2026-07-24T00:00:00Z\",\"target\":\"darwin/arm64\",\"release_key_id\":\"sha256:abc\",\"release_public_key\":\"must-not-appear\"},\"components\":{\"a2a_cli\":{\"version\":\"0.3.1-dev\",\"tag\":\"none\",\"upstream_commit\":\"fedcba9876543210\",\"built_at\":\"2026-07-24T00:00:00Z\",\"target\":\"darwin/arm64\",\"patch_set\":\"sha256:patch\",\"artifact_sha256\":\"sha256:artifact\",\"artifact_size\":12345}}}\n"
	if string(got) != want {
		t.Fatalf("JSON() = %q, want %q", got, want)
	}
	if strings.Contains(string(got), "private-path") {
		t.Fatalf("JSON() exposed release endpoint configuration: %q", got)
	}
}

func TestBundleTextReportsDistinctKadoAndA2AIdentities(t *testing.T) {
	t.Parallel()

	info := Info{
		Version: "1.2.3", Commit: "kado-commit", Date: "2026-08-27T00:00:00Z", Target: "windows/amd64",
		A2A: A2AInfo{
			Version: "0.3.1-dev", Tag: "none", UpstreamCommit: "a2a-commit",
			Date: "2026-08-26T00:00:00Z", Target: "windows/amd64",
			PatchSet: "sha256:patch", ArtifactSHA256: "sha256:artifact", ArtifactSize: 42,
		},
	}
	got := info.BundleText()
	for _, value := range []string{"Kado:\n", "A2A CLI:\n", "kado-commit", "a2a-commit", "artifact size: 42"} {
		if !strings.Contains(got, value) {
			t.Fatalf("BundleText() missing %q: %q", value, got)
		}
	}
	if strings.Contains(info.Line(), "a2a-commit") {
		t.Fatalf("Line() unexpectedly includes A2A identity: %q", info.Line())
	}
}
