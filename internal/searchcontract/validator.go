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
	contractOnce sync.Once
	contractV1   compiledContract
	contractErr  error
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
		if major != 1 {
			return Document{}, &UnsupportedVersionError{Major: major}
		}
	}
	if version.SchemaVersion != SchemaVersion {
		return Document{}, ErrInvalid
	}

	contract, err := compiledV1()
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
		validateSemantics(document) != nil ||
		validateJSONLD(jsonLDValue, document, contract.context) != nil {
		return Document{}, ErrInvalid
	}
	return document, nil
}

func compiledV1() (compiledContract, error) {
	contractOnce.Do(func() {
		assets, err := loadReleasedAssets()
		if err != nil || validateSemanticArtifact(assets.semantics) != nil {
			contractErr = ErrInvalid
			return
		}
		schemaValue, err := jsonschema.UnmarshalJSON(bytes.NewReader(assets.schema))
		if err != nil {
			contractErr = ErrInvalid
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		if err := compiler.AddResource(SchemaURL, schemaValue); err != nil {
			contractErr = ErrInvalid
			return
		}
		schema, err := compiler.Compile(SchemaURL)
		if err != nil {
			contractErr = ErrInvalid
			return
		}
		var context any
		if err := decodeJSON(assets.context, &context); err != nil {
			contractErr = ErrInvalid
			return
		}
		contractV1 = compiledContract{schema: schema, context: context}
	})
	return contractV1, contractErr
}

type localContextLoader struct {
	context any
}

func (loader localContextLoader) LoadDocument(url string) (*ld.RemoteDocument, error) {
	if url != ContextURL {
		return nil, fmt.Errorf("remote JSON-LD documents are disabled")
	}
	return &ld.RemoteDocument{
		DocumentURL: ContextURL,
		Document:    loader.context,
	}, nil
}

func validateJSONLD(value any, document Document, context any) error {
	compacted, err := expandAndCompactJSONLD(value, context)
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
	}
	return nil
}

func expandAndCompactJSONLD(value any, context any) (map[string]any, error) {
	processor := ld.NewJsonLdProcessor()
	options := ld.NewJsonLdOptions("")
	options.ProcessingMode = ld.JsonLd_1_1
	options.DocumentLoader = localContextLoader{context: context}
	expanded, err := processor.Expand(value, options)
	if err != nil || len(expanded) == 0 || !canonicalExpandedTerms(expanded) {
		return nil, ErrInvalid
	}
	compacted, err := processor.Compact(expanded, ContextURL, options)
	if err != nil || compacted["@context"] != ContextURL {
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

func canonicalExpandedTerms(value any) bool {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			if !canonicalExpandedTerms(child) {
				return false
			}
		}
	case map[string]any:
		for term, child := range value {
			if !strings.HasPrefix(term, "@") &&
				!strings.HasPrefix(term, "https://schema.org/") &&
				!strings.HasPrefix(
					term,
					"https://kado.so/vocab/search-document/v1#",
				) {
				return false
			}
			if term != "@value" && !canonicalExpandedTerms(child) {
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
