package agentauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
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
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	completed := false
	defer func() {
		if !completed {
			_ = os.WriteFile(filepath.Join(handoff, "consumer-complete.gen.json"), []byte("{\"success\":false}\n"), 0600)
		}
	}()
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
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(400)
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
	defer func() { peer.CloseClientConnections(); peer.Close() }()
	endpoint := peer.URL + "/mcp"
	write(filepath.Join(handoff, "mcp-ready.gen.json"), map[string]string{"endpoint": endpoint})
	t.Log("controlled MCP ready; waiting for real provider and app")
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
	auth.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	// Product links deliberately use the canonical public origin. Route that
	// origin through a child-process-only CONNECT proxy and temporary CA instead
	// of rewriting documents or weakening the CLI's exact-origin checks.
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"kado.so"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, public, private)
	if err != nil {
		t.Fatal(err)
	}
	auth.server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}}
	auth.server.StartTLS()
	listenerAddress := auth.server.Listener.Addr().String()
	auth.server.URL = "https://kado.so"
	transportProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "kado.so:443" {
			w.WriteHeader(403)
			return
		}
		upstream, err := net.DialTimeout("tcp", listenerAddress, 5*time.Second)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		downstream, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		defer downstream.Close()
		defer upstream.Close()
		_, _ = io.WriteString(downstream, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { _, _ = io.Copy(upstream, downstream); upstream.Close() }()
		_, _ = io.Copy(downstream, upstream)
	}))
	defer transportProxy.Close()
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
	env := append(payload.NodeEnvironment(os.Environ()), "SSL_CERT_FILE="+ca, "KADO_CONFIG_DIR="+configDir, "KADO_MCP_HOME_DIR="+mcpHome, "KADO_MAINTENANCE_CHILD=1", "NO_COLOR=1", "HTTPS_PROXY="+transportProxy.URL, "HTTP_PROXY="+transportProxy.URL, "NO_PROXY=127.0.0.1,localhost")
	run := func(executable string, args ...string) []byte {
		t.Helper()
		c := exec.CommandContext(ctx, executable, args...)
		c.Env = env
		if len(args) > 0 && args[0] == "search" {
			// This existing cryptographic fixture implements fresh enrollment,
			// not persisted management-credential recovery. Give each Search
			// process its own agent identity. MCP uses the anonymous test peer;
			// authenticated profile reuse is covered by exact-candidate tests.
			freshConfig, err := os.MkdirTemp(root, "search-auth-")
			if err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(freshConfig, "config.json"), map[string]any{"base_url": auth.issuer(), "credentials": map[string]string{"backend": "file"}})
			c.Env = append(append([]string{}, env...), "KADO_CONFIG_DIR="+freshConfig)
			auth.mu.Lock()
			auth.nonceConsumed = false
			auth.mu.Unlock()
		}
		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		if err := c.Run(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, stderr.String())
		}
		return stdout.Bytes()
	}
	installed := filepath.Join(root, "install space ü", "kado")
	if err := os.Mkdir(filepath.Dir(installed), 0700); err != nil {
		t.Fatal(err)
	}
	run(binary, "__install-bundle", "--directory", release, "--target", installed)
	t.Log("complete signed candidate installed")
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		c := exec.CommandContext(cleanup, installed, "mcp", "close", "@qualification")
		c.Env = env
		_ = c.Run()
	}()
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
	v2Client := auth.server.Client()
	v2Transport := v2Client.Transport.(*http.Transport).Clone()
	v2Transport.Proxy = nil
	v2Transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, listenerAddress)
	}
	v2Client.Transport = v2Transport
	defer v2Transport.CloseIdleConnections()
	response, err := v2Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Contains(v2, []byte(endpoint)) {
		t.Fatalf("public v2: status=%d body=%s err=%v", response.StatusCode, v2, err)
	}
	argsFile := filepath.Join(root, "args.json")
	write(argsFile, map[string]string{"text": "hello ü"})
	schema := run(installed, "mcp", "tools-get", selected, "selected_tool", "--no-profile", "--transport", "http", "--insecure", "--json")
	if !bytes.Contains(schema, []byte("inputSchema")) {
		t.Fatal("selected schema missing")
	}
	for _, command := range [][]string{
		{"mcp", "tools-call", selected, "selected_tool", "--args-file", argsFile, "--no-profile", "--transport", "http", "--insecure", "--json"},
		{"mcp", "connect", selected, "@qualification", "--no-profile", "--transport", "http", "--insecure"},
		{"mcp", "@qualification", "tools-call", "selected_tool", "--args-file", argsFile, "--json"},
	} {
		out := run(installed, command...)
		if strings.Contains(strings.Join(command, " "), "tools-call") && !bytes.Contains(out, []byte("real Search handoff completed")) {
			t.Fatal("MCP result missing")
		}
	}
	run(installed, "mcp", "close", "@qualification")
	if calls.Load() != 2 {
		t.Fatalf("tool calls=%d", calls.Load())
	}
	write(filepath.Join(handoff, "consumer-complete.gen.json"), map[string]any{"success": true, "installed": true, "direct_call": true, "named_call": true, "calls": calls.Load(), "cli_json": true, "cli_jsonl": true, "public_v2": true})
	completed = true
	t.Log("signed installed candidate: real Search JSON/JSONL, public v1/v2, direct and named MCP calls passed")
}
