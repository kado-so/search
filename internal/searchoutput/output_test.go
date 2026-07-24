package searchoutput

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/searchcontract"
	"github.com/mattn/go-runewidth"
)

func TestCanonicalJSONIsTheExactUnmodifiedServerDocument(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"complete", "complete_page"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			canonical := releasedFixture(t, name)
			rendered, err := Render(canonical, nil, Options{Mode: ModeJSON})
			if err != nil {
				t.Fatalf("Render(JSON) error = %v", err)
			}
			if !bytes.Equal(rendered, canonical) {
				t.Fatalf("Render(JSON) changed canonical %s bytes", name)
			}
			rendered[0] ^= 1
			if bytes.Equal(rendered, canonical) {
				t.Fatalf("Render(JSON) returned aliased %s bytes", name)
			}
		})
	}
}

func TestHumanAndJSONLReleasedGoldens(t *testing.T) {
	t.Parallel()

	canonical := releasedFixture(t, "complete")
	for name, options := range map[string]Options{
		"complete.human.golden": {Mode: ModeHuman, Width: 72},
		"complete.jsonl.golden": {Mode: ModeJSONL, Width: 72},
	} {
		name := name
		options := options
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rendered, err := Render(canonical, nil, options)
			if err != nil {
				t.Fatalf("Render(%s) error = %v", options.Mode, err)
			}
			assertGolden(t, name, rendered)
		})
	}
}

func TestJSONLPreservesAllArbitraryDataAndExplicitPagination(t *testing.T) {
	t.Parallel()

	canonical := releasedFixture(t, "complete")
	document, err := searchcontract.Validate(canonical)
	if err != nil || document.ResultSet == nil {
		t.Fatalf("Validate(complete) error = %v", err)
	}
	rendered, err := Render(canonical, nil, Options{Mode: ModeJSONL})
	if err != nil {
		t.Fatalf("Render(JSONL) error = %v", err)
	}
	lines := bytes.Split(bytes.TrimSuffix(rendered, []byte{'\n'}), []byte{'\n'})
	if len(lines) != len(document.ResultSet.Items)+2 {
		t.Fatalf("JSONL line count = %d", len(lines))
	}
	for index, item := range document.ResultSet.Items {
		var line struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(lines[index+1], &line); err != nil {
			t.Fatalf("json.Unmarshal(result %d) error = %v", index, err)
		}
		if line.Kind != "result" || !sameJSON(line.Data, item.Data) {
			t.Fatalf("JSONL result %d did not preserve data", index)
		}
	}
	var pagination struct {
		Kind       string                     `json:"kind"`
		Pagination map[string]json.RawMessage `json:"pagination"`
		Links      map[string]json.RawMessage `json:"links"`
	}
	if err := json.Unmarshal(lines[len(lines)-1], &pagination); err != nil {
		t.Fatalf("json.Unmarshal(pagination) error = %v", err)
	}
	for _, field := range []string{
		"kind",
		"page_size",
		"returned",
		"total",
		"has_more",
		"next_cursor",
		"previous_cursor",
	} {
		if _, ok := pagination.Pagination[field]; !ok {
			t.Fatalf("JSONL pagination omitted %s", field)
		}
	}
	for _, field := range []string{"self", "next", "previous"} {
		if _, ok := pagination.Links[field]; !ok {
			t.Fatalf("JSONL links omitted %s", field)
		}
	}
}

func TestJSONLRetainsExplicitPagePaginationShape(t *testing.T) {
	t.Parallel()

	rendered, err := Render(
		releasedFixture(t, "complete_page"),
		nil,
		Options{Mode: ModeJSONL},
	)
	if err != nil {
		t.Fatalf("Render(page JSONL) error = %v", err)
	}
	lines := bytes.Split(bytes.TrimSuffix(rendered, []byte{'\n'}), []byte{'\n'})
	var pagination struct {
		Kind       string                     `json:"kind"`
		Pagination map[string]json.RawMessage `json:"pagination"`
	}
	if err := json.Unmarshal(lines[len(lines)-1], &pagination); err != nil {
		t.Fatalf("json.Unmarshal(page pagination) error = %v", err)
	}
	for _, field := range []string{
		"kind",
		"page",
		"page_size",
		"returned",
		"total",
		"has_more",
		"next_page",
		"previous_page",
	} {
		if _, ok := pagination.Pagination[field]; !ok {
			t.Fatalf("JSONL page pagination omitted %s", field)
		}
	}
	if pagination.Kind != "pagination" ||
		string(pagination.Pagination["kind"]) != `"page"` {
		t.Fatalf("JSONL page pagination = %s", lines[len(lines)-1])
	}
}

func TestEveryLifecycleFixtureRendersDeterministically(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"queued",
		"running",
		"needs_input",
		"complete",
		"complete_page",
		"failed",
		"canceled",
	} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			canonical := releasedFixture(t, name)
			for _, mode := range []Mode{ModeHuman, ModeJSONL} {
				first, err := Render(canonical, nil, Options{Mode: mode, Width: 64})
				if err != nil {
					t.Fatalf("Render(%s, %s) error = %v", name, mode, err)
				}
				second, err := Render(canonical, nil, Options{Mode: mode, Width: 64})
				if err != nil || !bytes.Equal(first, second) {
					t.Fatalf("Render(%s, %s) is not deterministic", name, mode)
				}
				document, _ := searchcontract.Validate(canonical)
				if !bytes.Contains(first, []byte(document.State.Status)) {
					t.Fatalf("Render(%s, %s) omitted lifecycle status", name, mode)
				}
			}
		})
	}
}

func TestHumanOutputHonorsUnicodeDisplayWidthAndStripsTerminalControls(t *testing.T) {
	t.Parallel()

	var value map[string]any
	if err := json.Unmarshal(releasedFixture(t, "complete"), &value); err != nil {
		t.Fatalf("json.Unmarshal(complete) error = %v", err)
	}
	item := value["result_set"].(map[string]any)["items"].([]any)[0].(map[string]any)
	item["name"] = "\u001b[31m検索検索検索検索検索検索検索検索検索検索"
	item["summary"] = "مرحبا بالعالم — résumé — 👩🏽‍💻 — deterministic wrapping"
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(Unicode fixture) error = %v", err)
	}
	rendered, err := Render(encoded, nil, Options{Mode: ModeHuman, Width: 40})
	if err != nil {
		t.Fatalf("Render(Unicode human) error = %v", err)
	}
	if bytes.Contains(rendered, []byte{0x1b}) {
		t.Fatalf("human output retained terminal escape: %q", rendered)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(rendered), "\n"), "\n") {
		if width := runewidth.StringWidth(line); width > 40 {
			t.Fatalf("human line width = %d: %q", width, line)
		}
	}
}

func TestOutputFailsBeforeReturningBytesForInvalidVersionWidthOrPageCount(t *testing.T) {
	t.Parallel()

	canonical := releasedFixture(t, "queued")
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatalf("json.Unmarshal(queued) error = %v", err)
	}
	value["schema_version"] = "kado.search-document.v2"
	unsupported, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(v2) error = %v", err)
	}
	for _, test := range []struct {
		name    string
		primary []byte
		pages   [][]byte
		options Options
	}{
		{
			name:    "unsupported version",
			primary: unsupported,
			options: Options{Mode: ModeJSON},
		},
		{
			name:    "narrow terminal",
			primary: canonical,
			options: Options{Mode: ModeHuman, Width: 39},
		},
		{
			name:    "too many pages",
			primary: canonical,
			pages:   make([][]byte, 101),
			options: Options{Mode: ModeJSONL},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rendered, err := Render(test.primary, test.pages, test.options)
			if err == nil || rendered != nil {
				t.Fatalf("Render(%s) bytes=%q error=%v", test.name, rendered, err)
			}
		})
	}
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("KADO_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(testdata) error = %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden %s differs; rerun with KADO_UPDATE_GOLDEN=1", name)
	}
}

func releasedFixture(t *testing.T, name string) []byte {
	t.Helper()
	value, err := searchcontract.ReleasedFixture(name)
	if err != nil {
		t.Fatalf("ReleasedFixture(%s) error = %v", name, err)
	}
	return value
}

func sameJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil ||
		json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
