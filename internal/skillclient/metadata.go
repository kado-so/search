// Package skillclient installs and refreshes signed Kado Search skills.
package skillclient

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/kado-so/search/internal/releaseclient"
)

const (
	SchemaVersion   = "kado.skill-release.v1"
	SkillName       = "kado-search"
	MaxMetadataSize = 64 << 10
	MaxArchiveSize  = 8 << 20
)

var versionPattern = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?$`,
)

type Archive struct {
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Metadata struct {
	SchemaVersion     string  `json:"schema_version"`
	Name              string  `json:"name"`
	Version           string  `json:"version"`
	MinimumCLIVersion string  `json:"minimum_cli_version"`
	Archive           Archive `json:"archive"`
}

func CanonicalMetadata(metadata Metadata) ([]byte, error) {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func VerifyMetadata(
	encoded []byte,
	signature []byte,
	publicKeyText string,
	metadataURL string,
) (Metadata, error) {
	if len(encoded) == 0 || len(encoded) > MaxMetadataSize {
		return Metadata{}, errors.New("skill metadata is invalid")
	}
	public, err := releaseclient.ParsePublicKey(publicKeyText)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(public, encoded, signature) {
		return Metadata{}, errors.New("skill metadata signature is invalid")
	}
	var metadata Metadata
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return Metadata{}, errors.New("skill metadata is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Metadata{}, errors.New("skill metadata is invalid")
	}
	canonical, err := CanonicalMetadata(metadata)
	if err != nil || !bytes.Equal(canonical, encoded) ||
		metadata.Validate(metadataURL) != nil {
		return Metadata{}, errors.New("skill metadata is invalid")
	}
	return metadata, nil
}

func (metadata Metadata) Validate(metadataURL string) error {
	if metadata.SchemaVersion != SchemaVersion ||
		metadata.Name != SkillName ||
		!versionPattern.MatchString(metadata.Version) ||
		!versionPattern.MatchString(metadata.MinimumCLIVersion) ||
		metadata.Archive.Size <= 0 ||
		metadata.Archive.Size > MaxArchiveSize ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(metadata.Archive.SHA256) {
		return errors.New("skill metadata is invalid")
	}
	base, err := url.Parse(metadataURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return errors.New("skill metadata is invalid")
	}
	archive, err := url.Parse(metadata.Archive.URL)
	if err != nil || archive.Scheme != "https" ||
		!strings.EqualFold(archive.Host, base.Host) ||
		path.Base(archive.Path) != "kado-search.tar.gz" ||
		archive.RawQuery != "" || archive.Fragment != "" {
		return errors.New("skill metadata is invalid")
	}
	return nil
}
