// Package buildinfo owns the metadata stamped into release binaries.
package buildinfo

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

const (
	maxMetadataRunes = 48
	VersionSchema    = "kado.version.v1"
)

// These values are overridden at build time with -ldflags -X.
var (
	Version            = "dev"
	Commit             = "unknown"
	Date               = "unknown"
	Target             = "unknown"
	ReleasePublicKey   = ""
	ReleaseKeyID       = "unknown"
	ReleaseMetadataURL = ""
	InstallChannel     = "unknown"
	A2AVersion         = "unknown"
	A2ATag             = "none"
	A2AUpstreamCommit  = "unknown"
	A2ADate            = "unknown"
	A2ATarget          = "unknown"
	A2APatchSet        = "unknown"
	A2AArtifactSHA256  = "unknown"
	A2AArtifactSize    = "unknown"
)

// A2AInfo is the non-secret identity of the bundled official A2A CLI.
type A2AInfo struct {
	Version        string
	Tag            string
	UpstreamCommit string
	Date           string
	Target         string
	PatchSet       string
	ArtifactSHA256 string
	ArtifactSize   int64
}

// VersionReport is the closed automation contract for an installed Kado pair.
type VersionReport struct {
	SchemaVersion string            `json:"schema_version"`
	Kado          KadoVersion       `json:"kado"`
	Components    VersionComponents `json:"components"`
}

// KadoVersion identifies the Kado executable and its release verifier.
type KadoVersion struct {
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	BuiltAt      string `json:"built_at"`
	Target       string `json:"target"`
	ReleaseKeyID string `json:"release_key_id"`
	PublicKey    string `json:"release_public_key"`
}

// VersionComponents contains independently versioned bundled executables.
type VersionComponents struct {
	A2ACLI A2AComponentVersion `json:"a2a_cli"`
}

// A2AComponentVersion identifies the exact bundled A2A source, patch, and executable.
type A2AComponentVersion struct {
	Version        string `json:"version"`
	Tag            string `json:"tag"`
	UpstreamCommit string `json:"upstream_commit"`
	BuiltAt        string `json:"built_at"`
	Target         string `json:"target"`
	PatchSet       string `json:"patch_set"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	ArtifactSize   int64  `json:"artifact_size"`
}

// Info is the safe, bounded build metadata exposed by the CLI.
type Info struct {
	Version            string  `json:"version"`
	Commit             string  `json:"commit"`
	Date               string  `json:"built_at"`
	Target             string  `json:"target"`
	ReleaseKeyID       string  `json:"release_key_id"`
	ReleasePublicKey   string  `json:"-"`
	ReleaseMetadataURL string  `json:"-"`
	InstallChannel     string  `json:"-"`
	A2A                A2AInfo `json:"-"`
}

// Current returns the metadata attached to this binary.
func Current() Info {
	return Info{
		Version:            boundedToken(Version),
		Commit:             boundedToken(Commit),
		Date:               boundedToken(Date),
		Target:             targetValue(Target),
		ReleaseKeyID:       boundedTokenLength(ReleaseKeyID, 80),
		ReleasePublicKey:   strings.TrimSpace(ReleasePublicKey),
		ReleaseMetadataURL: strings.TrimSpace(ReleaseMetadataURL),
		InstallChannel:     boundedToken(InstallChannel),
		A2A: A2AInfo{
			Version:        boundedToken(A2AVersion),
			Tag:            boundedToken(A2ATag),
			UpstreamCommit: boundedToken(A2AUpstreamCommit),
			Date:           boundedToken(A2ADate),
			Target:         componentTarget(A2ATarget, Target),
			PatchSet:       boundedTokenLength(A2APatchSet, 80),
			ArtifactSHA256: boundedTokenLength(A2AArtifactSHA256, 80),
			ArtifactSize:   positiveInt64(A2AArtifactSize),
		},
	}
}

func positiveInt64(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

// Line returns the single-line, human-readable version representation.
func (info Info) Line() string {
	return fmt.Sprintf(
		"kado %s commit=%s built=%s",
		boundedToken(info.Version),
		boundedToken(info.Commit),
		boundedToken(info.Date),
	)
}

// BundleText returns the bounded human-readable distribution report.
func (info Info) BundleText() string {
	return fmt.Sprintf(
		"Kado:\n"+
			"  version: %s\n"+
			"  commit: %s\n"+
			"  built: %s\n"+
			"  target: %s\n"+
			"  release key: %s\n"+
			"A2A CLI:\n"+
			"  version: %s\n"+
			"  tag: %s\n"+
			"  upstream commit: %s\n"+
			"  built: %s\n"+
			"  target: %s\n"+
			"  patch set: %s\n"+
			"  artifact sha256: %s\n"+
			"  artifact size: %d\n",
		boundedToken(info.Version),
		boundedToken(info.Commit),
		boundedToken(info.Date),
		targetValue(info.Target),
		boundedTokenLength(info.ReleaseKeyID, 80),
		boundedToken(info.A2A.Version),
		boundedToken(info.A2A.Tag),
		boundedToken(info.A2A.UpstreamCommit),
		boundedToken(info.A2A.Date),
		componentTarget(info.A2A.Target, info.Target),
		boundedTokenLength(info.A2A.PatchSet, 80),
		boundedTokenLength(info.A2A.ArtifactSHA256, 80),
		max(info.A2A.ArtifactSize, 0),
	)
}

// Report returns the bounded, non-secret distribution identity.
func (info Info) Report() VersionReport {
	return VersionReport{
		SchemaVersion: VersionSchema,
		Kado: KadoVersion{
			Version:      boundedToken(info.Version),
			Commit:       boundedToken(info.Commit),
			BuiltAt:      boundedToken(info.Date),
			Target:       targetValue(info.Target),
			ReleaseKeyID: boundedTokenLength(info.ReleaseKeyID, 80),
			PublicKey:    boundedTokenLength(info.ReleasePublicKey, 64),
		},
		Components: VersionComponents{A2ACLI: A2AComponentVersion{
			Version:        boundedToken(info.A2A.Version),
			Tag:            boundedToken(info.A2A.Tag),
			UpstreamCommit: boundedToken(info.A2A.UpstreamCommit),
			BuiltAt:        boundedToken(info.A2A.Date),
			Target:         componentTarget(info.A2A.Target, info.Target),
			PatchSet:       boundedTokenLength(info.A2A.PatchSet, 80),
			ArtifactSHA256: boundedTokenLength(info.A2A.ArtifactSHA256, 80),
			ArtifactSize:   max(info.A2A.ArtifactSize, 0),
		}},
	}
}

// JSON returns deterministic, non-secret distribution provenance.
func (info Info) JSON() ([]byte, error) {
	encoded, err := json.Marshal(info.Report())
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func componentTarget(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "unknown" {
		return targetValue(fallback)
	}
	return targetValue(value)
}

func targetValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "unknown" {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	return boundedToken(value)
}

func boundedToken(value string) string {
	return boundedTokenLength(value, maxMetadataRunes)
}

func boundedTokenLength(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}

	output := make([]rune, 0, min(len([]rune(value)), limit))
	for _, character := range value {
		if len(output) == limit {
			break
		}
		if character < '!' || character > '~' {
			output = append(output, '?')
			continue
		}
		output = append(output, character)
	}
	return string(output)
}
