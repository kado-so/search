package searchcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"

	"github.com/kado-so/search/internal/searchcontract/testfixture"
)

func TestReleasedFixturesValidateThroughSchemaJSONLDAndSemantics(t *testing.T) {
	t.Parallel()

	names, err := testfixture.Names()
	if err != nil {
		t.Fatalf("testfixture.Names() error = %v", err)
	}
	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			encoded := fixtureBytes(t, name)
			document, err := Validate(encoded)
			if err != nil {
				t.Fatalf("Validate(%s) error = %v", name, err)
			}
			if document.SchemaVersion != SchemaVersion ||
				document.Context != ContextURL ||
				document.Links.Schema != SchemaURL {
				t.Fatalf("Validate(%s) identity = %#v", name, document)
			}
		})
	}
}

func TestCompleteFixturePreservesHeterogeneousArbitraryJSONData(t *testing.T) {
	t.Parallel()

	document, err := Validate(fixtureBytes(t, "complete"))
	if err != nil || document.ResultSet == nil {
		t.Fatalf("Validate(complete) document=%#v error=%v", document, err)
	}
	want := []string{"object", "array", "scalar", "null"}
	got := make([]string, 0, len(document.ResultSet.Items))
	for _, item := range document.ResultSet.Items {
		var value any
		if err := json.Unmarshal(item.Data, &value); err != nil {
			t.Fatalf("json.Unmarshal(data) error = %v", err)
		}
		switch {
		case value == nil:
			got = append(got, "null")
		case reflect.ValueOf(value).Kind() == reflect.Map:
			got = append(got, "object")
		case reflect.ValueOf(value).Kind() == reflect.Slice:
			got = append(got, "array")
		default:
			got = append(got, "scalar")
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arbitrary JSON shapes = %q, want %q", got, want)
	}
}

func TestJSONLD11CompactionAcceptsZeroOneAndManyResultItems(t *testing.T) {
	t.Parallel()

	for _, count := range []int{0, 1, 4} {
		count := count
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			t.Parallel()

			value := completeFixtureValue(t)
			setCompleteItems(value, resultItems(value)[:count])
			encoded := mustMarshalDocument(t, value)
			if _, err := Validate(encoded); err != nil {
				t.Fatalf("Validate(%d items) error = %v", count, err)
			}
			compacted := compactDocument(t, encoded)
			resultSet, ok := compacted["result_set"].(map[string]any)
			if !ok {
				t.Fatalf("compacted result_set = %#v", compacted["result_set"])
			}

			items, exists := resultSet["items"]
			switch count {
			case 0:
				values, ok := items.([]any)
				if !exists || !ok || len(values) != 0 {
					t.Fatalf("zero compacted items = %#v, want empty array", items)
				}
			case 1:
				if _, ok := items.(map[string]any); !ok {
					t.Fatalf("one compacted item = %T, want object", items)
				}
			default:
				values, ok := items.([]any)
				if !ok || len(values) != count {
					t.Fatalf("many compacted items = %#v, want %d-value array", items, count)
				}
			}
		})
	}
}

func TestJSONLD11CompactionRoundTripsArbitraryItemData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data any
	}{
		{
			name: "object",
			data: map[string]any{
				"nested": []any{float64(1), true, nil},
			},
		},
		{
			name: "array",
			data: []any{"alpha", map[string]any{"enabled": false}, nil},
		},
		{name: "scalar", data: float64(27)},
		{name: "null", data: nil},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			value := completeFixtureValue(t)
			item := resultItems(value)[0].(map[string]any)
			item["data"] = test.data
			setCompleteItems(value, []any{item})
			encoded := mustMarshalDocument(t, value)
			document, err := Validate(encoded)
			if err != nil || document.ResultSet == nil ||
				len(document.ResultSet.Items) != 1 {
				t.Fatalf("Validate(%s) document=%#v error=%v", test.name, document, err)
			}

			compacted := compactDocument(t, encoded)
			resultSet := compacted["result_set"].(map[string]any)
			compactedItem, ok := resultSet["items"].(map[string]any)
			if !ok {
				t.Fatalf("compacted %s item = %T, want object", test.name, resultSet["items"])
			}
			compactedData, exists := compactedItem["data"]
			if !exists || !reflect.DeepEqual(compactedData, test.data) {
				t.Fatalf(
					"compacted %s data = %#v (exists %t), want %#v",
					test.name,
					compactedData,
					exists,
					test.data,
				)
			}
			if !sameJSONValue(document.ResultSet.Items[0].Data, compactedData) {
				t.Fatalf("typed %s data did not survive processor round-trip", test.name)
			}
		})
	}
}

func TestJSONLD11CompactionAuditOfOptionsAndLinks(t *testing.T) {
	t.Parallel()

	var needsInput map[string]any
	if err := json.Unmarshal(fixtureBytes(t, "needs_input"), &needsInput); err != nil {
		t.Fatalf("json.Unmarshal(needs-input) error = %v", err)
	}
	question := needsInput["state"].(map[string]any)["question"].(map[string]any)
	question["options"] = []any{"Web"}
	needsInputEncoded := mustMarshalDocument(t, needsInput)
	if _, err := Validate(needsInputEncoded); err != nil {
		t.Fatalf("Validate(single option) error = %v", err)
	}
	compactedNeedsInput := compactDocument(t, needsInputEncoded)
	compactedQuestion := compactedNeedsInput["state"].(map[string]any)["question"].(map[string]any)
	if compactedQuestion["options"] != "Web" {
		t.Fatalf("single compacted option = %#v, want scalar", compactedQuestion["options"])
	}

	completeEncoded := fixtureBytes(t, "complete")
	if _, err := Validate(completeEncoded); err != nil {
		t.Fatalf("Validate(complete) error = %v", err)
	}
	compactedComplete := compactDocument(t, completeEncoded)
	rootLinks, ok := compactedComplete["links"].(map[string]any)
	if !ok ||
		rootLinks["self"] == nil ||
		rootLinks["schema"] == nil ||
		rootLinks["context"] == nil {
		t.Fatalf("compacted root links = %#v", compactedComplete["links"])
	}
	resultLinks := compactedComplete["result_set"].(map[string]any)["links"].(map[string]any)
	if _, ok := resultLinks["next"].(string); !ok {
		t.Fatalf("compacted next link = %#v, want scalar string", resultLinks["next"])
	}
	if previous, exists := resultLinks["previous"]; exists {
		t.Fatalf("compacted null previous link = %#v, want omitted", previous)
	}
}

func TestUnsupportedMajorIsClearAndDoesNotEchoDocumentData(t *testing.T) {
	t.Parallel()

	var value map[string]any
	if err := json.Unmarshal(fixtureBytes(t, "queued"), &value); err != nil {
		t.Fatalf("json.Unmarshal(queued) error = %v", err)
	}
	value["schema_version"] = "kado.search-document.v27"
	value["search"].(map[string]any)["query"] = "Bearer should-not-appear"
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(v27) error = %v", err)
	}
	_, err = Validate(encoded)
	var unsupported *UnsupportedVersionError
	if !errors.As(err, &unsupported) ||
		unsupported.Major != 27 ||
		bytes.Contains([]byte(err.Error()), []byte("Bearer")) {
		t.Fatalf("Validate(v27) error = %T %v", err, err)
	}
}

func TestSchemaRejectsClosedEnvelopeDrift(t *testing.T) {
	t.Parallel()

	var value map[string]any
	if err := json.Unmarshal(fixtureBytes(t, "complete"), &value); err != nil {
		t.Fatalf("json.Unmarshal(complete) error = %v", err)
	}
	value["provider_internal"] = true
	value["result_set"].(map[string]any)["items"].([]any)[0].(map[string]any)["typo"] = true
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(drift) error = %v", err)
	}
	if _, err := Validate(encoded); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate(drift) error = %v, want ErrInvalid", err)
	}
}

func TestEveryReleasedSemanticRuleHasARejectingMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code    string
		fixture string
		mutate  func(map[string]any)
	}{
		{"RESULT_RETURNED_COUNT_MISMATCH", "complete", func(value map[string]any) {
			pagination(value)["returned"] = float64(0)
		}},
		{"RESULT_RETURNED_EXCEEDS_PAGE_SIZE", "complete", func(value map[string]any) {
			pagination(value)["page_size"] = float64(3)
		}},
		{"RESULT_TOTAL_BELOW_RETURNED", "complete", func(value map[string]any) {
			pagination(value)["total"] = float64(3)
		}},
		{"PAGE_TOTAL_BELOW_OFFSET", "complete_page", func(value map[string]any) {
			pagination(value)["page"] = float64(2)
			pagination(value)["previous_page"] = float64(1)
			resultLinks(value)["previous"] = "https://kado.so/search/search_complete_page/previous"
		}},
		{"RESULT_SELF_LINK_MISMATCH", "complete", func(value map[string]any) {
			resultLinks(value)["self"] = "https://kado.so/search/different/example"
		}},
		{"CURSOR_PREVIOUS_LINK_MISMATCH", "complete", func(value map[string]any) {
			pagination(value)["previous_cursor"] = "cursor_previous"
		}},
		{"PAGE_PREVIOUS_REFERENCE_MISMATCH", "complete_page", func(value map[string]any) {
			pagination(value)["page"] = float64(2)
		}},
		{"PAGE_NEXT_REFERENCE_MISMATCH", "complete_page", func(value map[string]any) {
			pagination(value)["has_more"] = true
			pagination(value)["next_page"] = float64(3)
			resultLinks(value)["next"] = "https://kado.so/search/search_complete_page/next"
		}},
		{"PAGE_HAS_MORE_TOTAL_MISMATCH", "complete_page", func(value map[string]any) {
			pagination(value)["total"] = float64(100)
		}},
		{"PAGE_PREVIOUS_LINK_SELF_REFERENCE", "complete_page", func(value map[string]any) {
			pagination(value)["page"] = float64(2)
			pagination(value)["total"] = float64(20)
			pagination(value)["previous_page"] = float64(1)
			resultLinks(value)["previous"] = resultLinks(value)["self"]
		}},
		{"PAGE_NEXT_LINK_SELF_REFERENCE", "complete_page", func(value map[string]any) {
			pagination(value)["total"] = float64(100)
			pagination(value)["has_more"] = true
			pagination(value)["next_page"] = float64(2)
			resultLinks(value)["next"] = resultLinks(value)["self"]
		}},
		{"PAGE_RELATION_LINK_DUPLICATE", "complete_page", func(value map[string]any) {
			pagination(value)["page"] = float64(2)
			pagination(value)["total"] = float64(100)
			pagination(value)["has_more"] = true
			pagination(value)["previous_page"] = float64(1)
			pagination(value)["next_page"] = float64(3)
			resultLinks(value)["previous"] = "https://kado.so/search/search_complete_page/relation"
			resultLinks(value)["next"] = resultLinks(value)["previous"]
		}},
		{"ITEM_POSITION_DUPLICATE", "complete", func(value map[string]any) {
			items := resultItems(value)
			items[1].(map[string]any)["position"] = items[0].(map[string]any)["position"]
		}},
		{"ITEM_POSITION_NOT_INCREASING", "complete", func(value map[string]any) {
			items := resultItems(value)
			items[1].(map[string]any)["position"] = float64(3)
			items[2].(map[string]any)["position"] = float64(2)
		}},
		{"SEARCH_STARTED_BEFORE_CREATED", "running", func(value map[string]any) {
			search := value["search"].(map[string]any)
			search["created_at"] = "2026-07-23T00:00:00.000000002Z"
			search["started_at"] = "2026-07-23T00:00:00.000000001Z"
		}},
		{"SEARCH_COMPLETED_BEFORE_STARTED", "complete", func(value map[string]any) {
			search := value["search"].(map[string]any)
			search["started_at"] = "2026-07-23T00:00:00.000000002Z"
			search["completed_at"] = "2026-07-23T00:00:00.000000001Z"
		}},
		{"SEARCH_COMPLETED_BEFORE_CREATED", "failed", func(value map[string]any) {
			search := value["search"].(map[string]any)
			delete(search, "started_at")
			search["created_at"] = "2026-07-23T00:00:00.000000002Z"
			search["completed_at"] = "2026-07-23T00:00:00.000000001Z"
		}},
		{"TIMESTAMP_INVALID", "queued", func(value map[string]any) {
			value["search"].(map[string]any)["created_at"] = "2026-02-30T00:00:00Z"
		}},
	}
	if len(tests) != len(semanticIssueCodes) {
		t.Fatalf("semantic mutation count = %d, want %d", len(tests), len(semanticIssueCodes))
	}
	for index, test := range tests {
		test := test
		if test.code != semanticIssueCodes[index] {
			t.Fatalf("semantic mutation %d = %s, want %s", index, test.code, semanticIssueCodes[index])
		}
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()
			var value map[string]any
			if err := json.Unmarshal(fixtureBytes(t, test.fixture), &value); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", test.fixture, err)
			}
			test.mutate(value)
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("json.Marshal(%s) error = %v", test.code, err)
			}
			var document Document
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatalf("json.Unmarshal(document) error = %v", err)
			}
			if err := validateSemantics(document); !errors.Is(err, ErrInvalid) {
				t.Fatalf("validateSemantics(%s) error = %v", test.code, err)
			}
			if _, err := Validate(encoded); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate(%s) error = %v", test.code, err)
			}
		})
	}
}

func TestPinnedGeneratedAssetsMatchAuthoritativeSibling(t *testing.T) {
	t.Parallel()

	source := os.Getenv("KADO_APP_SEARCH_CONTRACT")
	if source == "" {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("runtime.Caller() failed")
		}
		source = filepath.Clean(
			filepath.Join(filepath.Dir(file), "../../../kado-app/contracts/search-document/v1"),
		)
	}
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		t.Skip("authoritative sibling kado-app contract is not checked out")
	} else if err != nil {
		t.Fatalf("os.Stat(%s) error = %v", source, err)
	}
	for name := range generatedCompressedAssets {
		want, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, err)
		}
		got, err := generatedAsset(name)
		if err != nil {
			t.Fatalf("generatedAsset(%s) error = %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("generated asset %s differs from authoritative sibling", name)
		}
	}
}

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	value, err := testfixture.Load(name)
	if err != nil {
		t.Fatalf("testfixture.Load(%s) error = %v", name, err)
	}
	return value
}

func pagination(value map[string]any) map[string]any {
	return value["result_set"].(map[string]any)["pagination"].(map[string]any)
}

func resultLinks(value map[string]any) map[string]any {
	return value["result_set"].(map[string]any)["links"].(map[string]any)
}

func resultItems(value map[string]any) []any {
	return value["result_set"].(map[string]any)["items"].([]any)
}

func completeFixtureValue(t *testing.T) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(fixtureBytes(t, "complete"), &value); err != nil {
		t.Fatalf("json.Unmarshal(complete) error = %v", err)
	}
	return value
}

func setCompleteItems(value map[string]any, items []any) {
	resultSet := value["result_set"].(map[string]any)
	resultSet["items"] = items
	pagination := resultSet["pagination"].(map[string]any)
	pagination["page_size"] = float64(max(1, len(items)))
	pagination["returned"] = float64(len(items))
	pagination["total"] = nil
	pagination["has_more"] = false
	pagination["next_cursor"] = nil
	pagination["previous_cursor"] = nil
	links := resultSet["links"].(map[string]any)
	links["next"] = nil
	links["previous"] = nil
}

func mustMarshalDocument(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(document) error = %v", err)
	}
	return encoded
}

func compactDocument(t *testing.T, encoded []byte) map[string]any {
	t.Helper()
	contract, err := compiledV1()
	if err != nil {
		t.Fatalf("compiledV1() error = %v", err)
	}
	var value any
	if err := decodeJSON(encoded, &value); err != nil {
		t.Fatalf("decodeJSON(document) error = %v", err)
	}
	compacted, err := expandAndCompactJSONLD(value, contract.context)
	if err != nil {
		t.Fatalf("expandAndCompactJSONLD() error = %v", err)
	}
	return compacted
}
