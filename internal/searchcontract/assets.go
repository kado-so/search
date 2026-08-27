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
	SchemaVersionV1 = "kado.search-document.v1"
	SchemaVersionV2 = "kado.search-document.v2"
	ContextURLV1    = "https://kado.so/contexts/search-document/v1.jsonld"
	ContextURLV2    = "https://kado.so/contexts/search-document/v2.jsonld"
	SchemaURLV1     = "https://kado.so/schemas/search-document/v1.json"
	SchemaURLV2     = "https://kado.so/schemas/search-document/v2.json"
	SemanticRulesV1 = "kado.search-document-semantics.v1"
	SemanticRulesV2 = "kado.search-document-semantics.v2"

	// The Search client requests v1 by default. These aliases preserve its
	// existing public constants while validation and rendering accept v1/v2.
	SchemaVersion = SchemaVersionV1
	ContextURL    = ContextURLV1
	SchemaURL     = SchemaURLV1
	SemanticRules = SemanticRulesV1

	maxAssetBytes      = 256 * 1024
	maxGeneratedAssets = 8
)

type contractIdentity struct {
	version       string
	schemaVersion string
	contextURL    string
	schemaURL     string
	semanticRules string
	manifestHash  string
}

var contractIdentities = map[string]contractIdentity{
	SchemaVersionV1: {
		version: "v1", schemaVersion: SchemaVersionV1,
		contextURL: ContextURLV1, schemaURL: SchemaURLV1,
		semanticRules: SemanticRulesV1,
		manifestHash:  "cb4344c5058880e95b41d6c6412b209fa8d16417e04d6eb5119d3771a3d9def9",
	},
	SchemaVersionV2: {
		version: "v2", schemaVersion: SchemaVersionV2,
		contextURL: ContextURLV2, schemaURL: SchemaURLV2,
		semanticRules: SemanticRulesV2,
		manifestHash:  "81a52f900de7cc3ffd6b42fa8b1742163fa0e63b6b40d0c7a94eada408a125f0",
	},
}

type releaseManifest struct {
	Contract      string                   `json:"contract"`
	Version       string                   `json:"version"`
	SchemaVersion string                   `json:"schema_version"`
	Artifacts     map[string]manifestEntry `json:"artifacts"`
}

type manifestEntry struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type releasedAssets struct {
	identity  contractIdentity
	manifest  releaseManifest
	schema    []byte
	context   []byte
	semantics []byte
}

var (
	assetsOnce      sync.Once
	assetsByVersion map[string]releasedAssets
	assetsErr       error
)

func loadReleasedAssets(schemaVersion string) (releasedAssets, error) {
	assetsOnce.Do(func() {
		assetsByVersion, assetsErr = decodeReleasedAssets()
	})
	if assetsErr != nil {
		return releasedAssets{}, assetsErr
	}
	assets, ok := assetsByVersion[schemaVersion]
	if !ok {
		return releasedAssets{}, fmt.Errorf("unsupported Search contract version")
	}
	return assets, nil
}

func decodeReleasedAssets() (map[string]releasedAssets, error) {
	if len(generatedCompressedAssets) == 0 ||
		len(generatedCompressedAssets) > maxGeneratedAssets {
		return nil, fmt.Errorf("invalid generated Search contract assets")
	}
	decoded := make(map[string]releasedAssets, len(contractIdentities))
	for schemaVersion, identity := range contractIdentities {
		prefix := identity.version + "/"
		manifestBytes, err := generatedAsset(prefix + "manifest.gen.json")
		if err != nil || checksum(manifestBytes) != identity.manifestHash {
			return nil, fmt.Errorf("invalid generated Search contract manifest")
		}
		var manifest releaseManifest
		if err := decodeJSON(manifestBytes, &manifest); err != nil ||
			manifest.Contract != "kado.search-document" ||
			manifest.Version != identity.version ||
			manifest.SchemaVersion != identity.schemaVersion {
			return nil, fmt.Errorf("invalid generated Search contract manifest")
		}
		schema, err := checkedGeneratedAsset(prefix, manifest.Artifacts["schema"])
		if err != nil {
			return nil, err
		}
		context, err := checkedGeneratedAsset(prefix, manifest.Artifacts["context"])
		if err != nil {
			return nil, err
		}
		semantics, err := checkedGeneratedAsset(prefix, manifest.Artifacts["semantics"])
		if err != nil {
			return nil, err
		}
		if manifest.Artifacts["schema"].URL != identity.schemaURL ||
			manifest.Artifacts["context"].URL != identity.contextURL {
			return nil, fmt.Errorf("invalid generated Search contract URLs")
		}
		decoded[schemaVersion] = releasedAssets{
			identity: identity, manifest: manifest,
			schema: schema, context: context, semantics: semantics,
		}
	}
	return decoded, nil
}

func checkedGeneratedAsset(prefix string, entry manifestEntry) ([]byte, error) {
	if entry.Path == "" || entry.SHA256 == "" {
		return nil, fmt.Errorf("generated Search contract artifact is missing")
	}
	value, err := generatedAsset(prefix + entry.Path)
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
