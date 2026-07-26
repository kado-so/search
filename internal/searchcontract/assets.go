package searchcontract

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const (
	SchemaVersion      = "kado.search-document.v1"
	ContextURL         = "https://kado.so/contexts/search-document/v1.jsonld"
	SchemaURL          = "https://kado.so/schemas/search-document/v1.json"
	SemanticRules      = "kado.search-document-semantics.v1"
	pinnedManifestHash = "000f3f58ea4fcf2cc105e1f4904ea2d392df8088c79fa0cc0e7514436a7a2713"
	maxAssetBytes      = 256 * 1024
	maxGeneratedAssets = 16
)

type releaseManifest struct {
	Contract      string                   `json:"contract"`
	Version       string                   `json:"version"`
	SchemaVersion string                   `json:"schema_version"`
	Artifacts     map[string]manifestEntry `json:"artifacts"`
	Fixtures      map[string]manifestEntry `json:"fixtures"`
}

type manifestEntry struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type releasedAssets struct {
	manifest  releaseManifest
	schema    []byte
	context   []byte
	semantics []byte
}

var (
	assetsOnce sync.Once
	assetsV1   releasedAssets
	assetsErr  error
)

func loadReleasedAssets() (releasedAssets, error) {
	assetsOnce.Do(func() {
		assetsV1, assetsErr = decodeReleasedAssets()
	})
	return assetsV1, assetsErr
}

// ReleasedFixture returns an isolated authoritative fixture generated from the
// pinned release manifest. It supports local conformance/golden tests.
func ReleasedFixture(name string) ([]byte, error) {
	assets, err := loadReleasedAssets()
	if err != nil {
		return nil, ErrInvalid
	}
	entry, ok := assets.manifest.Fixtures[name]
	if !ok {
		return nil, ErrInvalid
	}
	value, err := checkedGeneratedAsset(entry)
	if err != nil {
		return nil, ErrInvalid
	}
	return append([]byte(nil), value...), nil
}

func decodeReleasedAssets() (releasedAssets, error) {
	if len(generatedCompressedAssets) == 0 ||
		len(generatedCompressedAssets) > maxGeneratedAssets {
		return releasedAssets{}, fmt.Errorf("invalid generated Search contract assets")
	}
	manifestBytes, err := generatedAsset("manifest.gen.json")
	if err != nil ||
		checksum(manifestBytes) != pinnedManifestHash {
		return releasedAssets{}, fmt.Errorf("invalid generated Search contract manifest")
	}
	var manifest releaseManifest
	if err := decodeJSON(manifestBytes, &manifest); err != nil ||
		manifest.Contract != "kado.search-document" ||
		manifest.Version != "v1" ||
		manifest.SchemaVersion != SchemaVersion {
		return releasedAssets{}, fmt.Errorf("invalid generated Search contract manifest")
	}
	schema, err := checkedGeneratedAsset(manifest.Artifacts["schema"])
	if err != nil {
		return releasedAssets{}, err
	}
	context, err := checkedGeneratedAsset(manifest.Artifacts["context"])
	if err != nil {
		return releasedAssets{}, err
	}
	semantics, err := checkedGeneratedAsset(manifest.Artifacts["semantics"])
	if err != nil {
		return releasedAssets{}, err
	}
	if manifest.Artifacts["schema"].URL != SchemaURL ||
		manifest.Artifacts["context"].URL != ContextURL {
		return releasedAssets{}, fmt.Errorf("invalid generated Search contract URLs")
	}
	return releasedAssets{
		manifest:  manifest,
		schema:    schema,
		context:   context,
		semantics: semantics,
	}, nil
}

func checkedGeneratedAsset(entry manifestEntry) ([]byte, error) {
	if entry.Path == "" || entry.SHA256 == "" {
		return nil, fmt.Errorf("generated Search contract artifact is missing")
	}
	value, err := generatedAsset(entry.Path)
	if err != nil || checksum(value) != entry.SHA256 {
		return nil, fmt.Errorf("generated Search contract artifact checksum failed")
	}
	return value, nil
}

func generatedAsset(name string) ([]byte, error) {
	encoded, ok := generatedCompressedAssets[name]
	if !ok || encoded == "" {
		return nil, fmt.Errorf("generated Search contract artifact is missing")
	}
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(compressed) > maxAssetBytes {
		return nil, fmt.Errorf("generated Search contract artifact is invalid")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("generated Search contract artifact is invalid")
	}
	defer func() { _ = reader.Close() }()
	value, err := io.ReadAll(io.LimitReader(reader, maxAssetBytes+1))
	if err != nil || len(value) > maxAssetBytes {
		return nil, fmt.Errorf("generated Search contract artifact is invalid")
	}
	return value, nil
}

func checksum(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func decodeJSON(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}
