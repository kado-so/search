package agentauth

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kado-so/search/internal/payload"
)

// The other two participants are the real worker qualification and the app's
// mcp-installed-cli.integration.test.ts. Only authentication and the external
// model/MCP boundaries are fixtures; no search response is substituted here.
func TestInstalledMCPCandidateFromRealSearch(t *testing.T) {
	binary, release, handoff := os.Getenv("KADO_MCP_E2E_BINARY"), os.Getenv("KADO_MCP_E2E_RELEASE"), os.Getenv("KADO_SEARCH_MCP_HANDOFF_DIRECTORY")
	if binary == "" {
		t.Skip("supply the signed candidate, release directory and live cross-repository handoff")
	}
	if runtime.GOOS != "linux" {
		t.Skip("process-scoped test CA trust is qualified on Linux")
	}
	for _, p := range []string{binary, release, handoff} {
		if !filepath.IsAbs(p) {
			t.Fatal("absolute qualification paths required")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root := t.TempDir()
	write := func(name string, value any) {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(name string, value any) {
		t.Helper()
		for {
			if encoded, err := os.ReadFile(filepath.Join(handoff, name)); err == nil && json.Unmarshal(encoded, value) == nil {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("cross-repository participant timed out: " + name)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	var calls atomic.Int32
	const access = "isolated-mcp-qualification-token"
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+access {
			w.WriteHeader(401)
			return
		}
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, ": ready\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if r.Method == "DELETE" {
			w.WriteHeader(200)
			return
		}
		var msg struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			} `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&msg) != nil {
			w.WriteHeader(400)
			return
		}
		if msg.ID == nil {
			w.WriteHeader(202)
			return
		}
		var result any
		switch msg.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "databridge-qualification", "version": "1"}}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "selected_tool", "description": "Read the qualification marker", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]string{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false}}}}
		case "tools/call":
			if msg.Params.Name != "selected_tool" || msg.Params.Arguments["text"] != "hello ü" {
				w.WriteHeader(400)
				return
			}
			calls.Add(1)
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "real Search handoff completed"}}}
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "Unsupported fixture method"}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}))
	defer peer.Close()
	endpoint := peer.URL + "/mcp"
	secret := filepath.Join(root, "client-secret")
	if err := os.WriteFile(secret, []byte("qualification-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/token" {
			w.WriteHeader(404)
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("resource") != endpoint || r.PostForm.Get("scope") != "read" {
			w.WriteHeader(400)
			return
		}
		id, password, basic := r.BasicAuth()
		if (!basic || id != "fixture" || password != "qualification-secret") && (r.PostForm.Get("client_id") != "fixture" || r.PostForm.Get("client_secret") != "qualification-secret") {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "scope": "read"})
	}))
	defer authority.Close()
	write(filepath.Join(handoff, "mcp-ready.gen.json"), map[string]string{"endpoint": endpoint})
	var app struct{ URL, Query, Endpoint string }
	wait("app-ready.gen.json", &app)
	if app.Endpoint != endpoint {
		t.Fatal("index handoff changed the endpoint")
	}
	proxyURL := app.URL
	// An optional loopback TCP bridge connects WSL to a Windows app listener.
	if bridge := os.Getenv("KADO_SEARCH_MCP_APP_PROXY_URL"); bridge != "" {
		proxyURL = bridge
	}
	target, err := url.Parse(proxyURL)
	if err != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" {
		t.Fatal("loopback app transport required")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	auth := newGoal4Server()
	auth.server.Close()
	auth.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search") {
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				w.WriteHeader(401)
				return
			}
			proxy.ServeHTTP(w, r)
			return
		}
		auth.handle(w, r)
	}))
	defer auth.close()
	ca := filepath.Join(root, "qualification-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: auth.server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	configDir, mcpHome := filepath.Join(root, "config"), filepath.Join(root, "mcp")
	for _, p := range []string{configDir, mcpHome} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(configDir, "config.json"), map[string]any{"base_url": auth.issuer(), "credentials": map[string]string{"backend": "file"}})
	env := append(payload.NodeEnvironment(os.Environ()), "SSL_CERT_FILE="+ca, "KADO_CONFIG_DIR="+configDir, "KADO_MCP_HOME_DIR="+mcpHome, "KADO_MAINTENANCE_CHILD=1", "NO_COLOR=1")
	run := func(executable string, args ...string) []byte {
		t.Helper()
		c := exec.CommandContext(ctx, executable, args...)
		c.Env = env
		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		if err := c.Run(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, stderr.String())
		}
		if bytes.Contains(stdout.Bytes(), []byte(access)) || bytes.Contains(stderr.Bytes(), []byte(access)) {
			t.Fatal("credential leaked into CLI output")
		}
		return stdout.Bytes()
	}
	installed := filepath.Join(root, "install space ü", "kado")
	run(binary, "__install-bundle", "--directory", release, "--target", installed)
	defer func() { c := exec.Command(installed, "mcp", "close", "@qualification"); c.Env = env; _ = c.Run() }()
	run(installed, "release", "verify", "--directory", release)
	run(installed, "a2a", "--output", "json", "version")
	run(installed, "mcp", "--version", "--json")
	document := run(installed, "search", "--json", "--timeout", "90s", app.Query)
	var decoded struct {
		ResultSet struct {
			Items []struct {
				Use *struct{ Protocol, Endpoint, Transport string } `json:"use"`
			} `json:"items"`
		} `json:"result_set"`
		Links map[string]any `json:"links"`
	}
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatal(err)
	}
	selected := ""
	for _, item := range decoded.ResultSet.Items {
		if item.Use != nil && item.Use.Protocol == "mcp" && item.Use.Endpoint == endpoint && item.Use.Transport == "streamable-http" {
			selected = item.Use.Endpoint
		}
	}
	if selected == "" {
		t.Fatalf("real Search result omitted indexed MCP reference: %s", document)
	}
	if calls.Load() != 0 {
		t.Fatal("search must not invoke an MCP tool")
	}
	jsonl := run(installed, "search", "--jsonl", "--timeout", "90s", app.Query)
	if !bytes.Contains(jsonl, []byte(endpoint)) {
		t.Fatal("JSONL lost MCP endpoint")
	}
	// The CLI negotiates public v1. Independently request the actual public v2
	// representation and verify its endpoint before using the same installed CLI.
	request, _ := http.NewRequestWithContext(ctx, "GET", auth.issuer()+"/search?"+url.Values{"q": {app.Query}}.Encode(), nil)
	request.Header.Set("Accept", "application/vnd.kado.search.v2+json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Authorization", "Bearer isolated-product-principal")
	response, err := auth.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Contains(v2, []byte(endpoint)) {
		t.Fatalf("public v2: status=%d body=%s err=%v", response.StatusCode, v2, err)
	}
	run(installed, "mcp", "login", selected, "--grant", "client-credentials", "--client-id", "fixture", "--client-secret-file", secret, "--token-endpoint", authority.URL+"/token", "--profile", "work", "--scope", "read")
	argsFile := filepath.Join(root, "args.json")
	write(argsFile, map[string]string{"text": "hello ü"})
	schema := run(installed, "mcp", "tools-get", selected, "selected_tool", "--profile", "work", "--transport", "http", "--insecure", "--json")
	if !bytes.Contains(schema, []byte("inputSchema")) {
		t.Fatal("selected schema missing")
	}
	for _, command := range [][]string{
		{"mcp", "tools-call", selected, "selected_tool", "--args-file", argsFile, "--profile", "work", "--transport", "http", "--insecure", "--json"},
		{"mcp", "connect", selected, "@qualification", "--profile", "work", "--transport", "http", "--insecure"},
		{"mcp", "@qualification", "tools-call", "selected_tool", "--args-file", argsFile, "--json"},
	} {
		out := run(installed, command...)
		if strings.Contains(strings.Join(command, " "), "tools-call") && !bytes.Contains(out, []byte("real Search handoff completed")) {
			t.Fatal("MCP result missing")
		}
	}
	run(installed, "mcp", "close", "@qualification")
	run(installed, "mcp", "logout", selected, "--profile", "work")
	if calls.Load() != 2 {
		t.Fatalf("tool calls=%d", calls.Load())
	}
	write(filepath.Join(handoff, "consumer-complete.gen.json"), map[string]any{"success": true, "installed": true, "direct_call": true, "named_call": true, "calls": calls.Load(), "cli_json": true, "cli_jsonl": true, "public_v2": true})
	t.Log("signed installed candidate: real Search JSON/JSONL, public v1/v2, direct and named MCP calls passed")
}
