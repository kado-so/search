package searchoutput

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/searchcontract/testfixture"
	"github.com/mattn/go-runewidth"
)

func TestMCPUseInEveryOutputAndContractVersion(t *testing.T) {
	for _, version := range []testfixture.Version{testfixture.V1, testfixture.V2} {
		for _, transport := range []string{"streamable-http", "sse"} {
			t.Run(string(version)+"/"+transport, func(t *testing.T) {
				var document map[string]any
				if err := json.Unmarshal(releasedVersionFixture(t, version, "complete_mcp"), &document); err != nil {
					t.Fatal(err)
				}
				item := document["result_set"].(map[string]any)["items"].([]any)[0].(map[string]any)
				use := map[string]any{"protocol": "mcp", "endpoint": "https://mcp.example.com/path/%2F", "transport": transport}
				item["use"] = use
				canonical, _ := json.Marshal(document)
				raw, err := Render(canonical, nil, Options{Mode: ModeJSON})
				if err != nil || !bytes.Equal(raw, canonical) {
					t.Fatalf("JSON changed: %v", err)
				}
				jsonl, err := Render(canonical, nil, Options{Mode: ModeJSONL})
				if err != nil {
					t.Fatal(err)
				}
				var result map[string]json.RawMessage
				if err := json.Unmarshal(bytes.Split(jsonl, []byte{'\n'})[1], &result); err != nil {
					t.Fatal(err)
				}
				want, _ := json.Marshal(use)
				if !sameJSON(result["use"], want) {
					t.Fatalf("JSONL use = %s", result["use"])
				}
				for _, width := range []int{40, 100} {
					human, err := Render(canonical, nil, Options{Mode: ModeHuman, Width: width})
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(human, []byte("Use (mcp):")) || !bytes.Contains(human, []byte("Transport: "+transport)) {
						t.Fatalf("human use missing: %s", human)
					}
					if width == 100 && !bytes.Contains(human, []byte(use["endpoint"].(string))) {
						t.Fatalf("endpoint changed: %s", human)
					}
					for _, line := range strings.Split(string(human), "\n") {
						if runewidth.StringWidth(line) > width {
							t.Fatalf("unbounded line: %q", line)
						}
					}
				}
				for _, invalid := range []any{
					map[string]any{"protocol": "mcp", "endpoint": "https://mcp.example.com/\u001b", "transport": transport},
					map[string]any{"protocol": "future", "endpoint": "https://mcp.example.com", "transport": transport},
				} {
					item["use"] = invalid
					encoded, _ := json.Marshal(document)
					for _, mode := range []Mode{ModeJSON, ModeJSONL, ModeHuman} {
						if b, err := Render(encoded, nil, Options{Mode: mode, Width: 80}); err == nil || len(b) != 0 {
							t.Fatalf("invalid use accepted: %s", b)
						}
					}
				}
			})
		}
	}
}
