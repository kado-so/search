package searchcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/piprate/json-gold/ld"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var versionPattern = regexp.MustCompile(`^kado\.search-document\.v([1-9][0-9]*)$`)

type compiledContract struct {
	schema  *jsonschema.Schema
	context any
}

var (
	contractsOnce      sync.Once
	contractsByVersion map[string]compiledContract
	contractsErr       error
)

func Validate(encoded []byte) (Document, error) {
	if len(encoded) == 0 || !utf8.Valid(encoded) {
		return Document{}, ErrInvalid
	}
	if err := rejectDuplicateMembers(encoded); err != nil {
		return Document{}, ErrInvalid
	}
	var version struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := decodeJSON(encoded, &version); err != nil {
		return Document{}, ErrInvalid
	}
	if match := versionPattern.FindStringSubmatch(version.SchemaVersion); match != nil {
		major, err := strconv.ParseUint(match[1], 10, 32)
		if err != nil {
			return Document{}, ErrInvalid
		}
		if major != 1 && major != 2 {
			return Document{}, &UnsupportedVersionError{Major: major}
		}
	}
	identity, supported := contractIdentities[version.SchemaVersion]
	if !supported {
		return Document{}, ErrInvalid
	}

	contract, err := compiledContractFor(version.SchemaVersion)
	if err != nil {
		return Document{}, ErrInvalid
	}
	schemaValue, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil || contract.schema.Validate(schemaValue) != nil {
		return Document{}, ErrInvalid
	}
	var jsonLDValue any
	if err := decodeJSON(encoded, &jsonLDValue); err != nil {
		return Document{}, ErrInvalid
	}
	var document Document
	if err := decodeJSON(encoded, &document); err != nil ||
		validateSemantics(document, identity) != nil ||
		validateJSONLD(jsonLDValue, document, contract.context) != nil {
		return Document{}, ErrInvalid
	}
	return document, nil
}

func compiledContractFor(schemaVersion string) (compiledContract, error) {
	contractsOnce.Do(func() {
		assetsV1, err := loadReleasedAssets(SchemaVersionV1)
		if err != nil || validateSemanticArtifact(assetsV1) != nil {
			contractsErr = ErrInvalid
			return
		}
		assetsV2, err := loadReleasedAssets(SchemaVersionV2)
		if err != nil || validateSemanticArtifact(assetsV2) != nil {
			contractsErr = ErrInvalid
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		for _, assets := range []releasedAssets{assetsV1, assetsV2} {
			schemaValue, schemaErr := jsonschema.UnmarshalJSON(bytes.NewReader(assets.schema))
			if schemaErr != nil || compiler.AddResource(assets.identity.schemaURL, schemaValue) != nil {
				contractsErr = ErrInvalid
				return
			}
		}
		contractsByVersion = make(map[string]compiledContract, 2)
		for _, assets := range []releasedAssets{assetsV1, assetsV2} {
			schema, compileErr := compiler.Compile(assets.identity.schemaURL)
			var context any
			if compileErr != nil || decodeJSON(assets.context, &context) != nil {
				contractsErr = ErrInvalid
				return
			}
			contractsByVersion[assets.identity.schemaVersion] = compiledContract{
				schema: schema, context: context,
			}
		}
	})
	if contractsErr != nil {
		return compiledContract{}, contractsErr
	}
	contract, ok := contractsByVersion[schemaVersion]
	if !ok {
		return compiledContract{}, ErrInvalid
	}
	return contract, nil
}

type localContextLoader struct {
	contextURL string
	context    any
}

func (loader localContextLoader) LoadDocument(url string) (*ld.RemoteDocument, error) {
	if url != loader.contextURL {
		return nil, fmt.Errorf("remote JSON-LD documents are disabled")
	}
	return &ld.RemoteDocument{
		DocumentURL: loader.contextURL,
		Document:    loader.context,
	}, nil
}

func validateJSONLD(value any, document Document, context any) error {
	identity := contractIdentities[document.SchemaVersion]
	compacted, err := expandAndCompactJSONLD(value, identity, context)
	if err != nil {
		return ErrInvalid
	}
	if compacted["schema_version"] != document.SchemaVersion {
		return ErrInvalid
	}
	search, ok := compacted["search"].(map[string]any)
	if !ok || search["query"] != document.Search.Query {
		return ErrInvalid
	}
	state, ok := compacted["state"].(map[string]any)
	if !ok || state["status"] != document.State.Status {
		return ErrInvalid
	}
	if document.ResultSet == nil {
		return nil
	}
	resultSet, ok := compacted["result_set"].(map[string]any)
	if !ok {
		return ErrInvalid
	}
	pagination, ok := resultSet["pagination"].(map[string]any)
	if !ok ||
		pagination["kind"] != document.ResultSet.Pagination.Kind ||
		pagination["has_more"] != document.ResultSet.Pagination.HasMore {
		return ErrInvalid
	}
	items := compactedValues(resultSet, "items")
	if len(items) != len(document.ResultSet.Items) {
		return ErrInvalid
	}
	for index, item := range items {
		object, ok := item.(map[string]any)
		if !ok || !sameJSONValue(document.ResultSet.Items[index].Data, object["data"]) {
			return ErrInvalid
		}
		compactedUse, hasUse := object["use"]
		if document.ResultSet.Items[index].Use == nil {
			if hasUse {
				return ErrInvalid
			}
			continue
		}
		encodedUse, marshalErr := json.Marshal(document.ResultSet.Items[index].Use)
		if marshalErr != nil || !hasUse || !sameJSONValue(encodedUse, compactedUse) {
			return ErrInvalid
		}
	}
	return nil
}

func expandAndCompactJSONLD(value any, identity contractIdentity, context any) (map[string]any, error) {
	processor := ld.NewJsonLdProcessor()
	options := ld.NewJsonLdOptions("")
	options.ProcessingMode = ld.JsonLd_1_1
	options.DocumentLoader = localContextLoader{contextURL: identity.contextURL, context: context}
	expanded, err := processor.Expand(value, options)
	if err != nil || len(expanded) == 0 || !canonicalExpandedTerms(expanded, identity) {
		return nil, ErrInvalid
	}
	compacted, err := processor.Compact(expanded, identity.contextURL, options)
	if err != nil || compacted["@context"] != identity.contextURL {
		return nil, ErrInvalid
	}
	return compacted, nil
}

// JSON-LD 1.1 compaction with compactArrays enabled and no @container can emit
// an empty array, a singleton directly, or an array for multiple values.
// Normalize those representations without changing the expanded values that
// were already checked above.
func compactedValues(object map[string]any, term string) []any {
	value, ok := object[term]
	if !ok {
		return nil
	}
	if values, ok := value.([]any); ok {
		return values
	}
	return []any{value}
}

func canonicalExpandedTerms(value any, identity contractIdentity) bool {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			if !canonicalExpandedTerms(child, identity) {
				return false
			}
		}
	case map[string]any:
		for term, child := range value {
			if !strings.HasPrefix(term, "@") &&
				!strings.HasPrefix(term, "https://schema.org/") &&
				!strings.HasPrefix(
					term,
					"https://kado.so/vocab/search-document/"+identity.version+"#",
				) {
				return false
			}
			if term != "@value" && !canonicalExpandedTerms(child, identity) {
				return false
			}
		}
	}
	return true
}

func sameJSONValue(encoded json.RawMessage, value any) bool {
	var original any
	if err := json.Unmarshal(encoded, &original); err != nil {
		return false
	}
	return reflect.DeepEqual(original, value)
}

func rejectDuplicateMembers(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalid
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalid
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
