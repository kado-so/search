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
	SchemaVersion   = "kado.release.v1"
	Product         = "kado"
	MaxMetadataSize = 1 << 20
	MaxArchiveSize  = 128 << 20
	MaxSupportSize  = 16 << 20
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

// Target identifies one supported executable package.
type Target struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	BinaryName    string `json:"binary_name"`
	ArchiveFormat string `json:"archive_format"`
	Binary        File   `json:"binary"`
	Archive       File   `json:"archive"`
	SBOM          File   `json:"sbom"`
}

// Metadata is the canonical signed release index.
type Metadata struct {
	SchemaVersion    string   `json:"schema_version"`
	Product          string   `json:"product"`
	Version          string   `json:"version"`
	Commit           string   `json:"commit"`
	BuiltAt          string   `json:"built_at"`
	Repository       string   `json:"repository"`
	InstallURL       string   `json:"install_url"`
	SigningAlgorithm string   `json:"signing_algorithm"`
	KeyID            string   `json:"signing_key_id"`
	SigningPublicKey string   `json:"signing_public_key"`
	Targets          []Target `json:"targets"`
	Checksums        File     `json:"checksums"`
	Provenance       File     `json:"provenance"`
	InstallGuide     File     `json:"install_guide"`
	InstallUnix      File     `json:"install_unix"`
	InstallPower     File     `json:"install_powershell"`
	UninstallUnix    File     `json:"uninstall_unix"`
	UninstallPower   File     `json:"uninstall_powershell"`
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
	if metadata.SigningPublicKey != PublicKeyText(public) {
		return Metadata{}, errInvalidSignature
	}
	if err := metadata.Validate(); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

// Validate rejects ambiguous, unsupported, or cross-origin release metadata.
func (metadata Metadata) Validate() error {
	if metadata.SchemaVersion != SchemaVersion ||
		metadata.Product != Product ||
		len(metadata.Version) > 48 ||
		!versionPattern.MatchString(metadata.Version) ||
		!commitPattern.MatchString(metadata.Commit) ||
		metadata.Repository != "https://github.com/kado-so/search" ||
		metadata.SigningAlgorithm != "Ed25519" ||
		metadata.SigningPublicKey == "" ||
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
	signingPublicKey, err := ParsePublicKey(metadata.SigningPublicKey)
	if err != nil {
		return errInvalidMetadata
	}
	signingKeyID, err := KeyID(signingPublicKey)
	if err != nil || signingKeyID != metadata.KeyID {
		return errInvalidMetadata
	}
	installURL, err := parseHTTPS(metadata.InstallURL)
	if err != nil {
		return errInvalidMetadata
	}
	files := []File{
		metadata.Checksums,
		metadata.Provenance,
		metadata.InstallGuide,
		metadata.InstallUnix,
		metadata.InstallPower,
		metadata.UninstallUnix,
		metadata.UninstallPower,
	}
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
		wantBinary := "kado"
		wantFormat := "tar.gz"
		if target.OS == "windows" {
			wantBinary = "kado.exe"
			wantFormat = "zip"
		}
		if target.BinaryName != wantBinary || target.ArchiveFormat != wantFormat {
			return errInvalidMetadata
		}
		files = append(files, target.Binary, target.Archive, target.SBOM)
	}
	for _, file := range files {
		if err := validateFile(file, installURL); err != nil {
			return err
		}
		if _, duplicate := seenFiles[file.Name]; duplicate {
			return errInvalidMetadata
		}
		seenFiles[file.Name] = struct{}{}
	}
	return nil
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

func validateFile(file File, installURL *url.URL) error {
	if !safeName.MatchString(file.Name) ||
		!hexDigestPattern.MatchString(file.SHA256) ||
		file.Size <= 0 {
		return errInvalidMetadata
	}
	parsed, err := parseHTTPS(file.URL)
	if err != nil ||
		!strings.EqualFold(parsed.Host, installURL.Host) ||
		parsed.Scheme != installURL.Scheme ||
		path.Base(parsed.Path) != file.Name {
		return errInvalidMetadata
	}
	return nil
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
