// Package skillclient verifies and reconciles signed Kado skill releases.
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
	CatalogSchemaVersion = "kado.skill-catalog.v1"
	SchemaVersion        = "kado.skill-release.v2"
	SkillName            = "kado-search" // retained for source compatibility
	MaxCatalogSize       = 256 << 10
	MaxMetadataSize      = 64 << 10
	MaxArchiveSize       = 8 << 20
)

var (
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?$`)
	namePattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	variantPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
)

type Archive struct {
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Metadata struct {
	SchemaVersion     string  `json:"schema_version"`
	Name              string  `json:"name"`
	Variant           string  `json:"variant"`
	Version           string  `json:"version"`
	MinimumCLIVersion string  `json:"minimum_cli_version"`
	Archive           Archive `json:"archive"`
}

type Catalog struct {
	SchemaVersion string         `json:"schema_version"`
	Revision      uint64         `json:"revision"`
	Skills        []CatalogSkill `json:"skills"`
}

type CatalogSkill struct {
	Name     string           `json:"name"`
	State    string           `json:"state"`
	Variants []CatalogVariant `json:"variants,omitempty"`
}

type CatalogVariant struct {
	ID          string   `json:"id"`
	Agents      []string `json:"agents"`
	MetadataURL string   `json:"metadata_url"`
}

func CanonicalMetadata(value Metadata) ([]byte, error) { return canonicalJSON(value) }
func CanonicalCatalog(value Catalog) ([]byte, error)   { return canonicalJSON(value) }

func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func VerifyCatalog(encoded, signature []byte, publicKeyText, catalogURL string) (Catalog, error) {
	var catalog Catalog
	if err := verifySignedJSON(encoded, signature, publicKeyText, MaxCatalogSize, &catalog); err != nil {
		return Catalog{}, err
	}
	canonical, _ := CanonicalCatalog(catalog)
	if !bytes.Equal(canonical, encoded) || catalog.Validate(catalogURL) != nil {
		return Catalog{}, ErrInvalidRelease
	}
	return catalog, nil
}

func VerifyMetadata(encoded, signature []byte, publicKeyText, metadataURL string, expected ...string) (Metadata, error) {
	var metadata Metadata
	if err := verifySignedJSON(encoded, signature, publicKeyText, MaxMetadataSize, &metadata); err != nil {
		return Metadata{}, err
	}
	canonical, _ := CanonicalMetadata(metadata)
	if !bytes.Equal(canonical, encoded) || metadata.Validate(metadataURL) != nil {
		return Metadata{}, ErrInvalidRelease
	}
	if len(expected) > 0 && metadata.Name != expected[0] {
		return Metadata{}, ErrInvalidRelease
	}
	if len(expected) > 1 && metadata.Variant != expected[1] {
		return Metadata{}, ErrInvalidRelease
	}
	return metadata, nil
}

func verifySignedJSON(encoded, signature []byte, publicKeyText string, limit int64, output any) error {
	if len(encoded) == 0 || int64(len(encoded)) > limit {
		return ErrInvalidRelease
	}
	public, err := releaseclient.ParsePublicKey(publicKeyText)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(public, encoded, signature) {
		return ErrInvalidRelease
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(output) != nil {
		return ErrInvalidRelease
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidRelease
	}
	return nil
}

func (catalog Catalog) Validate(rawURL string) error {
	base, err := secureURL(rawURL)
	if err != nil || catalog.SchemaVersion != CatalogSchemaVersion || catalog.Revision == 0 || len(catalog.Skills) == 0 {
		return ErrInvalidRelease
	}
	seen := map[string]bool{}
	for _, skill := range catalog.Skills {
		if !validSkillName(skill.Name) || seen[skill.Name] || (skill.State != "active" && skill.State != "retired") {
			return ErrInvalidRelease
		}
		seen[skill.Name] = true
		if skill.State == "retired" && len(skill.Variants) != 0 {
			return ErrInvalidRelease
		}
		if skill.State == "active" && len(skill.Variants) == 0 {
			return ErrInvalidRelease
		}
		variantSeen := map[string]bool{}
		selectors := map[string]bool{}
		for _, variant := range skill.Variants {
			if !variantPattern.MatchString(variant.ID) || variantSeen[variant.ID] || len(variant.Agents) == 0 {
				return ErrInvalidRelease
			}
			variantSeen[variant.ID] = true
			for _, agent := range variant.Agents {
				if !validAgentSelector(agent) || selectors[agent] {
					return ErrInvalidRelease
				}
				selectors[agent] = true
			}
			candidate, err := secureURL(variant.MetadataURL)
			if err != nil || !strings.EqualFold(candidate.Host, base.Host) {
				return ErrInvalidRelease
			}
		}
	}
	return nil
}

func (metadata Metadata) Validate(rawURL string) error {
	base, err := secureURL(rawURL)
	if err != nil || metadata.SchemaVersion != SchemaVersion || !validSkillName(metadata.Name) || !variantPattern.MatchString(metadata.Variant) || !versionPattern.MatchString(metadata.Version) || !versionPattern.MatchString(metadata.MinimumCLIVersion) || metadata.Archive.Size <= 0 || metadata.Archive.Size > MaxArchiveSize || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(metadata.Archive.SHA256) {
		return ErrInvalidRelease
	}
	archive, err := secureURL(metadata.Archive.URL)
	if err != nil || !strings.EqualFold(archive.Host, base.Host) || path.Base(archive.Path) != metadata.Name+".tar.gz" || archive.RawQuery != "" || archive.Fragment != "" {
		return ErrInvalidRelease
	}
	return nil
}

func secureURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, ErrInvalidRelease
	}
	return parsed, nil
}

func validSkillName(name string) bool {
	return namePattern.MatchString(name) && name != "." && name != ".."
}

func SelectVariant(skill CatalogSkill, agent string) (CatalogVariant, bool) {
	for _, variant := range skill.Variants {
		for _, selector := range variant.Agents {
			if selector == agent {
				return variant, true
			}
		}
	}
	for _, variant := range skill.Variants {
		for _, selector := range variant.Agents {
			if selector == "*" {
				return variant, true
			}
		}
	}
	return CatalogVariant{}, false
}
