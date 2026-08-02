package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const completedEnvelope = `{
  "schema_version":"agent-api.v1",
  "search_id":"app_search_1",
  "state":"completed",
  "result":{
    "schema_version":"agent-cli-json.v1",
    "search_id":"app_search_1",
    "search_url":"https://kado.so/search?q=invoice+automation",
    "state":"completed",
    "best_matches":[{
      "rank":1,"solution_id":"solution_1","name":"Invoice Flow",
      "summary":"Approves invoices.","solution_url":"https://example.com/solution",
      "why":["Matches approval"],"source_url":"https://example.com/source","score":92,
      "constraints":{"satisfied":["approval"],"violated":[]},
      "required_integrations":["Gmail"]
    }],
    "stretch_matches":[],"later_matches":[],"questions":[],"dimensions":[],"error":null,
    "pagination":{"total_available":1,"returned_count":1,"best_available":1,
      "stretch_available":0,"later_available":0,"later_offset":0,"later_limit":5,
      "later_returned":0,"next_later_offset":null,"previous_later_offset":null,
      "next_later_url":null,"previous_later_url":null},
    "continuation":{"status_url":"/api/agent/searches/app_search_1",
      "answers_url":"/api/agent/searches/app_search_1/answers",
      "dimensions_url":"/api/agent/searches/app_search_1/dimensions",
      "cancel_url":"/api/agent/searches/app_search_1/cancel","poll_after_ms":null,
      "can_refine":true,"can_cancel":false,"next_command":null}
  }
}`

func TestStartUsesAgentAPIContractAndEmitsCompactResult(t *testing.T) {
	t.Parallel()
	var body map[string]any
	var apiKey, agent string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/prefix/api/agent/searches" || request.Method != http.MethodPost {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		apiKey, agent = request.Header.Get("X-API-Key"), request.Header.Get("User-Agent")
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(completedEnvelope))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL + "/prefix")
	client, err := New(Options{BaseURL: base, HTTPClient: server.Client(), APIKey: "sk-kado-test-value", UserAgent: "kado/0.1.3"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Start(context.Background(), StartRequest{
		Query: "invoice automation", Version: "0.1.3",
		Wait:   WaitOptions{Enabled: true, TimeoutMS: 45_000, PollIntervalMS: 500},
		Limits: ResultLimits{BestMatches: 3, LaterMatches: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "sk-kado-test-value" || agent != "kado/0.1.3" {
		t.Fatalf("headers api=%q agent=%q", apiKey, agent)
	}
	if body["schema_version"] != APISchemaVersion || body["mode"] != "compact" || body["query"] != "invoice automation" {
		t.Fatalf("body=%#v", body)
	}
	wait := body["wait"].(map[string]any)
	if wait["until"] != "completed_or_terminal" || wait["timeout_ms"] != float64(45_000) {
		t.Fatalf("wait=%#v", wait)
	}
	if response.Result.SchemaVersion != JSONSchemaVersion || response.Result.SearchID == nil || *response.Result.SearchID != "app_search_1" || len(response.Result.BestMatches) != 1 {
		t.Fatalf("result=%#v", response.Result)
	}
	if strings.Contains(string(response.ResultJSON), "\n  ") || !strings.HasSuffix(string(response.ResultJSON), "\n") {
		t.Fatalf("result JSON is not compact: %q", response.ResultJSON)
	}
}

type rotatingAuthorization struct {
	mu    sync.Mutex
	calls []bool
}

func (source *rotatingAuthorization) Authorization(_ context.Context, refresh bool) (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls = append(source.calls, refresh)
	if refresh {
		return "Bearer refreshed-private-token", nil
	}
	return "Bearer initial-private-token", nil
}

func TestBearerAuthorizationRefreshesOnceAfterUnauthorized(t *testing.T) {
	t.Parallel()
	source := &rotatingAuthorization{}
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		writer.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(failedEnvelope("agent.auth_invalid", 401)))
			return
		}
		if request.Header.Get("Authorization") != "Bearer refreshed-private-token" {
			t.Errorf("authorization was not refreshed")
		}
		_, _ = writer.Write([]byte(completedEnvelope))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client, err := New(Options{BaseURL: base, HTTPClient: server.Client(), Authorization: source})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Status(context.Background(), "app_search_1", WaitOptions{}, ResultLimits{}); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(source.calls) != 2 || source.calls[0] || !source.calls[1] {
		t.Fatalf("requests=%d calls=%v", requests, source.calls)
	}
}

func TestLifecycleMethodsUseCanonicalKadoAppRoutes(t *testing.T) {
	t.Parallel()
	type seenRequest struct {
		method, path, query string
		body                map[string]any
	}
	var seen []seenRequest
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		item := seenRequest{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery}
		if request.Body != nil {
			_ = json.NewDecoder(request.Body).Decode(&item.body)
		}
		seen = append(seen, item)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(completedEnvelope))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client, err := New(Options{BaseURL: base, HTTPClient: server.Client(), APIKey: "sk-kado-test-value"})
	if err != nil {
		t.Fatal(err)
	}
	wait := WaitOptions{Enabled: true, TimeoutMS: 10_000, PollIntervalMS: 250}
	limits := ResultLimits{BestMatches: 2, StretchMatches: 3, LaterMatches: 4, LaterOffset: 5}
	if _, err = client.Status(context.Background(), "app_search_1", wait, limits); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Refine(context.Background(), RefineRequest{SearchID: "app_search_1", Dimensions: []DimensionUpdate{{ID: "budget_monthly_usd", Value: "200", Unit: "usd"}}, Wait: wait, Limits: limits}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Answer(context.Background(), AnswerRequest{SearchID: "app_search_1", Answers: []Answer{{QuestionID: "lead_volume", Answer: "750"}}, Wait: wait, Limits: limits}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Cancel(context.Background(), CancelRequest{SearchID: "app_search_1", Reason: "done"}); err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"/api/agent/searches/app_search_1", "/api/agent/searches/app_search_1/dimensions", "/api/agent/searches/app_search_1/answers", "/api/agent/searches/app_search_1/cancel"}
	for index, want := range wantPaths {
		if seen[index].path != want {
			t.Fatalf("path[%d]=%q want %q", index, seen[index].path, want)
		}
	}
	statusQuery, _ := url.ParseQuery(seen[0].query)
	if statusQuery.Get("wait") != "1" || statusQuery.Get("max_later_matches") != "4" || statusQuery.Get("later_offset") != "5" {
		t.Fatalf("status query=%v", statusQuery)
	}
	if seen[1].body["schema_version"] != APISchemaVersion || seen[1].body["mode"] != "compact" || seen[2].body["schema_version"] != APISchemaVersion || seen[3].body["reason"] != "done" {
		t.Fatalf("bodies=%#v", seen)
	}
}

func TestRejectsResponsesOutsideKadoAppContract(t *testing.T) {
	t.Parallel()
	for _, mutation := range []func(map[string]any){
		func(envelope map[string]any) { envelope["schema_version"] = "agent-api.v2" },
		func(envelope map[string]any) { delete(envelope["result"].(map[string]any), "later_matches") },
		func(envelope map[string]any) { delete(envelope["result"].(map[string]any), "pagination") },
		func(envelope map[string]any) { envelope["result"].(map[string]any)["state"] = "complete" },
	} {
		var value map[string]any
		if err := json.Unmarshal([]byte(completedEnvelope), &value); err != nil {
			t.Fatal(err)
		}
		mutation(value)
		encoded, _ := json.Marshal(value)
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(encoded)
		}))
		base, _ := url.Parse(server.URL)
		client, err := New(Options{BaseURL: base, HTTPClient: server.Client(), APIKey: "sk-kado-test-value"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Status(context.Background(), "app_search_1", WaitOptions{}, ResultLimits{}); err == nil || !errors.Is(err, ErrProtocol) {
			t.Fatalf("error=%v", err)
		}
		server.Close()
	}
}

func failedEnvelope(code string, status int) string {
	return `{"schema_version":"agent-api.v1","search_id":null,"state":"failed","result":{"schema_version":"agent-cli-json.v1","search_id":null,"search_url":null,"state":"failed","best_matches":[],"stretch_matches":[],"later_matches":[],"questions":[],"dimensions":[],"error":{"code":"` + code + `","message":"Authentication failed.","retryable":false,"http_status":` + strconv.Itoa(status) + `},"pagination":{"total_available":0,"returned_count":0,"best_available":0,"stretch_available":0,"later_available":0,"later_offset":0,"later_limit":0,"later_returned":0,"next_later_offset":null,"previous_later_offset":null,"next_later_url":null,"previous_later_url":null},"continuation":{"status_url":null,"poll_after_ms":null,"can_refine":false,"can_cancel":false,"next_command":null}}}`
}
