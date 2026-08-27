// Package releaseclient verifies and installs Kado CLI release artifacts.
package releaseclient

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion   = "kado.release.v2"
	Product         = "kado"
	A2ARepository   = "https://github.com/a2aproject/a2a-cli"
	A2AModule       = "github.com/a2aproject/a2a-cli"
	MaxMetadataSize = 1 << 20
	MaxArchiveSize  = 128 << 20
)

var (
	errInvalidMetadata  = errors.New("invalid release metadata")
	errInvalidSignature = errors.New("invalid release signature")
	hexDigestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionPattern      = regexp.MustCompile(
		`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`,
	)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	safeName      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	supported     = map[string]struct{}{
		"darwin/amd64":  {},
		"darwin/arm64":  {},
		"linux/amd64":   {},
		"linux/arm64":   {},
		"windows/amd64": {},
		"windows/arm64": {},
	}
)

// File identifies one immutable release artifact.
type File struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// EmbeddedArtifact identifies an executable authenticated by its containing
// release archive.
type EmbeddedArtifact struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// A2AComponent identifies the exact official source and transformation used
// for every bundled A2A CLI executable in a release.
type A2AComponent struct {
	Repository          string `json:"repository"`
	Module              string `json:"module"`
	Version             string `json:"version"`
	Tag                 string `json:"tag"`
	Commit              string `json:"commit"`
	SourceArchiveSHA256 string `json:"source_archive_sha256"`
	SourceTreeSHA256    string `json:"source_tree_sha256"`
	PatchedTreeSHA256   string `json:"patched_tree_sha256"`
	GoModSHA256         string `json:"go_mod_sha256"`
	GoSumSHA256         string `json:"go_sum_sha256"`
	LicenseSHA256       string `json:"license_sha256"`
	GoToolchain         string `json:"go_toolchain"`
	DisplayName         string `json:"display_name"`
	PatchSetSHA256      string `json:"patch_set_sha256"`
	BuiltAt             string `json:"built_at"`
}

// Components contains independently versioned software shipped by Kado.
type Components struct {
	A2ACLI A2AComponent `json:"a2a_cli"`
}

// Target identifies one supported executable package.
type Target struct {
	OS      string           `json:"os"`
	Arch    string           `json:"arch"`
	Archive File             `json:"archive"`
	Sidecar EmbeddedArtifact `json:"sidecar"`
	SBOM    File             `json:"sbom"`
}

// Metadata is the canonical signed release index.
type Metadata struct {
	SchemaVersion string     `json:"schema_version"`
	Product       string     `json:"product"`
	Version       string     `json:"version"`
	Commit        string     `json:"commit"`
	BuiltAt       string     `json:"built_at"`
	KeyID         string     `json:"signing_key_id"`
	Components    Components `json:"components"`
	Provenance    File       `json:"provenance"`
	Targets       []Target   `json:"targets"`
}

// CanonicalMetadata returns the only accepted release metadata encoding.
func CanonicalMetadata(metadata Metadata) ([]byte, error) {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// KeyID returns the stable identifier for a release verification key.
func KeyID(public ed25519.PublicKey) (string, error) {
	if len(public) != ed25519.PublicKeySize {
		return "", errInvalidSignature
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return "", errInvalidSignature
	}
	digest := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// PublicKeyText returns the non-secret encoding stamped into release binaries.
func PublicKeyText(public ed25519.PublicKey) string {
	return base64.RawStdEncoding.EncodeToString(public)
}

// ParsePublicKey parses the exact non-secret verifier key representation.
func ParsePublicKey(encoded string) (ed25519.PublicKey, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errInvalidSignature
	}
	return ed25519.PublicKey(decoded), nil
}

// VerifyMetadata authenticates canonical metadata before it is used.
func VerifyMetadata(
	encoded []byte,
	signature []byte,
	trustedPublicKey string,
) (Metadata, error) {
	if len(encoded) == 0 || len(encoded) > MaxMetadataSize {
		return Metadata{}, errInvalidMetadata
	}
	public, err := ParsePublicKey(trustedPublicKey)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(public, encoded, signature) {
		return Metadata{}, errInvalidSignature
	}
	var metadata Metadata
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return Metadata{}, errInvalidMetadata
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Metadata{}, errInvalidMetadata
	}
	canonical, err := CanonicalMetadata(metadata)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return Metadata{}, errInvalidMetadata
	}
	expectedKeyID, err := KeyID(public)
	if err != nil || metadata.KeyID != expectedKeyID {
		return Metadata{}, errInvalidSignature
	}
	if err := metadata.Validate(); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

// Validate rejects ambiguous or unsupported release metadata.
func (metadata Metadata) Validate() error {
	if metadata.SchemaVersion != SchemaVersion ||
		metadata.Product != Product ||
		len(metadata.Version) > 48 ||
		!versionPattern.MatchString(metadata.Version) ||
		!commitPattern.MatchString(metadata.Commit) ||
		!strings.HasPrefix(metadata.KeyID, "sha256:") ||
		len(metadata.Targets) != len(supported) {
		return errInvalidMetadata
	}
	builtAt, err := time.Parse(time.RFC3339, metadata.BuiltAt)
	if err != nil ||
		builtAt.Location() != time.UTC ||
		builtAt.Format(time.RFC3339) != metadata.BuiltAt {
		return errInvalidMetadata
	}
	if err := metadata.Components.A2ACLI.validate(metadata.BuiltAt); err != nil {
		return err
	}
	if metadata.Provenance.Name != "provenance.intoto.json" {
		return errInvalidMetadata
	}
	files := make([]File, 0, 1+2*len(metadata.Targets))
	files = append(files, metadata.Provenance)
	seenTargets := make(map[string]struct{}, len(metadata.Targets))
	seenFiles := make(map[string]struct{})
	if !sort.SliceIsSorted(metadata.Targets, func(left, right int) bool {
		if metadata.Targets[left].OS != metadata.Targets[right].OS {
			return metadata.Targets[left].OS < metadata.Targets[right].OS
		}
		return metadata.Targets[left].Arch < metadata.Targets[right].Arch
	}) {
		return errInvalidMetadata
	}
	for _, target := range metadata.Targets {
		key := target.OS + "/" + target.Arch
		if _, ok := supported[key]; !ok {
			return errInvalidMetadata
		}
		if _, duplicate := seenTargets[key]; duplicate {
			return errInvalidMetadata
		}
		seenTargets[key] = struct{}{}
		if _, _, ok := targetLayout(target.OS); !ok {
			return errInvalidMetadata
		}
		base := fmt.Sprintf("kado_%s_%s_%s", metadata.Version, target.OS, target.Arch)
		expectedArchive := base + ".tar.gz"
		if target.OS == "windows" {
			expectedArchive = base + ".zip"
		}
		if target.Archive.Name != expectedArchive ||
			target.SBOM.Name != base+".spdx.json" ||
			!validEmbeddedArtifact(target.Sidecar) {
			return errInvalidMetadata
		}
		files = append(files, target.Archive, target.SBOM)
	}
	for _, file := range files {
		if err := validateFile(file); err != nil {
			return err
		}
		if _, duplicate := seenFiles[file.Name]; duplicate {
			return errInvalidMetadata
		}
		seenFiles[file.Name] = struct{}{}
	}
	return nil
}

func (component A2AComponent) validate(releaseBuiltAt string) error {
	if component.Repository != A2ARepository ||
		component.Module != A2AModule ||
		len(component.Version) > 48 ||
		!versionPattern.MatchString(component.Version) ||
		!commitPattern.MatchString(component.Commit) ||
		component.DisplayName != "kado a2a" ||
		!regexp.MustCompile(`^go[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(component.GoToolchain) ||
		component.BuiltAt != releaseBuiltAt {
		return errInvalidMetadata
	}
	if component.Tag == "none" {
		if !strings.Contains(component.Version, component.Commit[:7]) {
			return errInvalidMetadata
		}
	} else if !regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`).MatchString(component.Tag) ||
		strings.TrimPrefix(component.Tag, "v") != component.Version {
		return errInvalidMetadata
	}
	for _, digest := range []string{
		component.SourceArchiveSHA256,
		component.SourceTreeSHA256,
		component.PatchedTreeSHA256,
		component.GoModSHA256,
		component.GoSumSHA256,
		component.LicenseSHA256,
		component.PatchSetSHA256,
	} {
		if !hexDigestPattern.MatchString(digest) {
			return errInvalidMetadata
		}
	}
	return nil
}

func validEmbeddedArtifact(artifact EmbeddedArtifact) bool {
	return hexDigestPattern.MatchString(artifact.SHA256) && artifact.Size > 0
}

// TargetFor selects an exact supported operating-system and architecture pair.
func (metadata Metadata) TargetFor(goos, goarch string) (Target, error) {
	for _, target := range metadata.Targets {
		if target.OS == goos && target.Arch == goarch {
			return target, nil
		}
	}
	return Target{}, fmt.Errorf("%w: unsupported platform", errInvalidMetadata)
}

// Digest returns a lowercase SHA-256 digest.
func Digest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

// VerifyFile verifies both the exact size and digest of an artifact.
func VerifyFile(file File, value []byte) error {
	if file.Size != int64(len(value)) || file.SHA256 != Digest(value) {
		return errors.New("release artifact verification failed")
	}
	return nil
}

// SortedTargets returns a stable copy ordered by operating system and architecture.
func SortedTargets(targets []Target) []Target {
	output := append([]Target(nil), targets...)
	sort.Slice(output, func(left, right int) bool {
		if output[left].OS != output[right].OS {
			return output[left].OS < output[right].OS
		}
		return output[left].Arch < output[right].Arch
	})
	return output
}

func validateFile(file File) error {
	if !safeName.MatchString(file.Name) ||
		!hexDigestPattern.MatchString(file.SHA256) ||
		file.Size <= 0 {
		return errInvalidMetadata
	}
	parsed, err := parseHTTPS(file.URL)
	if err != nil ||
		path.Base(parsed.Path) != file.Name {
		return errInvalidMetadata
	}
	return nil
}

func targetLayout(goos string) (binaryName, archiveFormat string, ok bool) {
	switch goos {
	case "windows":
		return "kado.exe", "zip", true
	case "darwin", "linux":
		return "kado", "tar.gz", true
	default:
		return "", "", false
	}
}

func parseHTTPS(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		parsed.Path == "" ||
		strings.Contains(parsed.EscapedPath(), "%") {
		return nil, errInvalidMetadata
	}
	return parsed, nil
}
