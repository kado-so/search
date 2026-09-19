package protocoltelemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

type fakeAuthorization struct {
	mu        sync.Mutex
	refreshes []bool
}

func (source *fakeAuthorization) Authorization(_ context.Context, refresh bool) (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.refreshes = append(source.refreshes, refresh)
	return "Bearer test-token", nil
}

func TestClientReportsBoundedEventAndRefreshesOnce(t *testing.T) {
	t.Parallel()
	requests := 0
	var captured Event
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/prefix/"+ingestionPath || request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected request: %s %s auth=%q", request.Method, request.URL.Path, request.Header.Get("Authorization"))
		}
		if requests == 1 {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	base, err := url.Parse(server.URL + "/prefix")
	if err != nil {
		t.Fatal(err)
	}
	authorization := &fakeAuthorization{}
	client, err := newClient(base, server.Client(), authorization)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		SchemaVersion: SchemaVersion, AttemptID: "attempt_1", Phase: PhaseStarted,
		Protocol: ProtocolMCP, Command: "tools-call", TargetKind: "endpoint",
		Target: "https://mcp.example/mcp", StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := client.Report(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if captured != event || requests != 2 {
		t.Fatalf("captured=%#v requests=%d", captured, requests)
	}
	if len(authorization.refreshes) != 2 || authorization.refreshes[0] || !authorization.refreshes[1] {
		t.Fatalf("refresh calls = %v", authorization.refreshes)
	}
}

func TestClientRejectsPayloadLikeCommandBeforeNetwork(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client, err := newClient(base, server.Client(), &fakeAuthorization{})
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		SchemaVersion: SchemaVersion, AttemptID: "attempt_1", Phase: PhaseStarted,
		Protocol: ProtocolA2A, Command: "send private message", StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := client.Report(context.Background(), event); err == nil {
		t.Fatal("Report() accepted unsafe command")
	}
	if requests != 0 {
		t.Fatalf("requests = %d", requests)
	}
}
