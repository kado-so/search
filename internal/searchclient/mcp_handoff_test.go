package searchclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kado-so/search/internal/searchcontract"
	"github.com/kado-so/search/internal/searchcontract/testfixture"
)

func TestMCPConsumerPreservesPublicDocumentBytes(t *testing.T) {
	for _, version := range []testfixture.Version{testfixture.V1, testfixture.V2} {
		t.Run(string(version), func(t *testing.T) {
			encoded, err := testfixture.LoadVersion(version, "complete_mcp")
			if err != nil {
				t.Fatal(err)
			}
			assertMCPConsumerDocument(t, encoded)
		})
	}
}

func TestRealIndexedMCPAppHandoff(t *testing.T) {
	directory := os.Getenv("KADO_SEARCH_MCP_PUBLIC_DOCUMENTS")
	if directory == "" {
		t.Skip("real indexed app handoff disabled")
	}
	for _, version := range []string{"v1", "v2"} {
		encoded, err := os.ReadFile(filepath.Join(directory, "public-"+version+".json"))
		if err != nil {
			t.Fatal(err)
		}
		assertMCPConsumerDocument(t, encoded)
	}
}

func assertMCPConsumerDocument(t *testing.T, encoded []byte) {
	t.Helper()
	var source searchcontract.Document
	if err := json.Unmarshal(encoded, &source); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assertMachineRequest(t, request, "Bearer token-one")
		// Existing HTTP negotiation is retained; the lifecycle client requests v1.
		response.Header().Set("Content-Type", CanonicalMediaType)
		_, _ = response.Write(encoded)
	}))
	defer server.Close()
	client := newIntegrationClient(t, server, &fakeAuthorizationSource{})
	var document Document
	var err error
	if source.SchemaVersion == searchcontract.SchemaVersionV1 {
		document, err = client.Search(context.Background(), source.Search.Query)
	} else {
		// v2 is admitted by the existing document consumer without changing the
		// network media-type negotiation (which is outside this goal).
		document, err = client.decodeDocument(encoded, source.Search.Query)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, document.Bytes()) {
		t.Fatal("consumer altered canonical document bytes")
	}
	validated, err := searchcontract.Validate(document.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range validated.ResultSet.Items {
		if item.Use != nil && item.Use.Protocol == "mcp" {
			if item.Use.Endpoint != "https://fixtures.invalid/databridge/mcp" || item.Use.Transport != "streamable-http" {
				t.Fatal("MCP reference changed")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("consumer lost MCP reference")
	}
	copy := document.Bytes()
	copy[0] = 'x'
	if !bytes.Equal(encoded, document.Bytes()) {
		t.Fatal("caller mutated canonical document")
	}
}
