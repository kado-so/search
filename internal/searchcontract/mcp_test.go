package searchcontract

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/searchcontract/testfixture"
)

func TestMCPUseRoundTripAndRejections(t *testing.T) {
	t.Parallel()
	for _, version := range []testfixture.Version{testfixture.V1, testfixture.V2} {
		t.Run(string(version), func(t *testing.T) {
			endpoint := "https://Example.com:443/a%2Fb/MCP/"
			for _, transport := range []string{"streamable-http", "sse"} {
				var value map[string]any
				if err := json.Unmarshal(fixtureVersionBytes(t, version, "complete_mcp"), &value); err != nil {
					t.Fatal(err)
				}
				resultItems(value)[0].(map[string]any)["use"] = map[string]any{"protocol": "mcp", "endpoint": endpoint, "transport": transport}
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				document, err := Validate(encoded)
				if err != nil {
					t.Fatal(err)
				}
				if use := document.ResultSet.Items[0].Use; use == nil || use.Protocol != "mcp" || use.Endpoint != endpoint || use.Transport != transport || use.AgentCard != "" {
					t.Fatalf("unexpected use: %+v", use)
				}
				roundTrip, err := json.Marshal(document.ResultSet.Items[0].Use)
				if err != nil {
					t.Fatal(err)
				}
				var use map[string]any
				if err := json.Unmarshal(roundTrip, &use); err != nil {
					t.Fatal(err)
				}
				if len(use) != 3 || use["protocol"] != "mcp" || use["endpoint"] != endpoint || use["transport"] != transport {
					t.Fatalf("typed use round-trip: %s", roundTrip)
				}
			}
			invalid := []any{
				nil,
				[]any{map[string]any{"protocol": "mcp", "endpoint": endpoint, "transport": "sse"}},
				map[string]any{"protocol": "unknown", "endpoint": endpoint, "transport": "sse"},
				map[string]any{"protocol": "mcp", "endpoint": endpoint},
				map[string]any{"protocol": "mcp", "endpoint": endpoint, "transport": "stdio"},
				map[string]any{"protocol": "mcp", "endpoint": endpoint, "transport": "sse", "agent_card": "https://example.com/card.json"},
				map[string]any{"protocol": "mcp", "endpoint": endpoint, "transport": "sse", "token": "secret"},
			}
			for _, invalidEndpoint := range []string{"http://example.com/mcp", "https://user:secret@example.com/mcp", "https://example.com/mcp?", "https://example.com/mcp#", "https://example.com/mcp?token=secret", "https://example.com/has space", "https://example.com/\\mcp", "https://:443/mcp", "https://example.com:99999/mcp", "https://[invalid]/mcp", "https://example.com/" + strings.Repeat("é", 1020)} {
				invalid = append(invalid, map[string]any{"protocol": "mcp", "endpoint": invalidEndpoint, "transport": "sse"})
			}
			for index, use := range invalid {
				var value map[string]any
				if err := json.Unmarshal(fixtureVersionBytes(t, version, "complete_mcp"), &value); err != nil {
					t.Fatal(err)
				}
				resultItems(value)[0].(map[string]any)["use"] = use
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := Validate(encoded); !errors.Is(err, ErrInvalid) {
					t.Fatalf("invalid reference %d accepted: %v", index, err)
				}
			}
		})
	}
}
