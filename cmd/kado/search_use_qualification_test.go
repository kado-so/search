package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
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
	"testing"
	"time"

	"github.com/kado-so/search/internal/searchcontract/testfixture"
	"github.com/kado-so/search/internal/searchoutput"
)

const a2aQualificationBinaryEnvironment = "KADO_A2A_QUALIFICATION_BINARY"

func TestSearchUseInvokesOfficialLocalEchoAgent(t *testing.T) {
	binary := os.Getenv(a2aQualificationBinaryEnvironment)
	if binary == "" {
		t.Skip("set KADO_A2A_QUALIFICATION_BINARY to a built paired Kado candidate")
	}
	if runtime.GOOS == "windows" {
		t.Skip("temporary CA injection is unavailable with Go's Windows system root loader")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("qualification binary is unavailable: %v", err)
	}

	echoURL, stopEcho := startOfficialEchoAgent(t, binary)
	t.Cleanup(stopEcho)
	cardURL, trustEnvironment := startTrustedAgentCardProxy(t, echoURL)

	for _, fixture := range []struct {
		version testfixture.Version
		name    string
	}{
		{version: testfixture.V1, name: "complete"},
		{version: testfixture.V2, name: "complete_no_questions"},
	} {
		fixture := fixture
		t.Run(string(fixture.version), func(t *testing.T) {
			searchJSON, err := testfixture.LoadVersion(fixture.version, fixture.name)
			if err != nil {
				t.Fatal(err)
			}
			searchJSON = replaceFirstAgentCard(t, searchJSON, cardURL)
			rendered, err := searchoutput.Render(searchJSON, nil, searchoutput.Options{Mode: searchoutput.ModeJSON})
			if err != nil {
				t.Fatalf("validate and render Search JSON: %v", err)
			}
			if string(rendered) != string(searchJSON) {
				t.Fatal("canonical Search JSON bytes changed before use selection")
			}

			use := extractFirstUse(t, rendered)
			if use.Protocol != "a2a" {
				t.Fatalf("unsupported result use protocol %q", use.Protocol)
			}
			message := "qualification message " + string(fixture.version)
			command := exec.Command(
				binary,
				"a2a",
				"--agent-card", use.AgentCard,
				"--output", "json",
				"send", message,
			)
			command.Env = append(os.Environ(), trustEnvironment...)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("kado a2a send failed: %v\n%s", err, output)
			}
			if !jsonContainsString(output, message) {
				t.Fatalf("official echo response omitted the caller message: %s", output)
			}
		})
	}
}

type resultUse struct {
	Protocol  string `json:"protocol"`
	AgentCard string `json:"agent_card"`
}

func extractFirstUse(t *testing.T, document []byte) resultUse {
	t.Helper()
	var value struct {
		ResultSet struct {
			Items []struct {
				Use *resultUse `json:"use"`
			} `json:"items"`
		} `json:"result_set"`
	}
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	if len(value.ResultSet.Items) == 0 || value.ResultSet.Items[0].Use == nil {
		t.Fatal("Search result omitted use")
	}
	return *value.ResultSet.Items[0].Use
}

func replaceFirstAgentCard(t *testing.T, document []byte, cardURL string) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	items := value["result_set"].(map[string]any)["items"].([]any)
	use := items[0].(map[string]any)["use"].(map[string]any)
	use["agent_card"] = cardURL
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func startOfficialEchoAgent(t *testing.T, binary string) (*url.URL, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "a2a", "--output", "json", "server", "--echo", "--quiet", "--port", port)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	stop := func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	}
	base, err := url.Parse("http://" + address)
	if err != nil {
		stop()
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response, requestErr := http.Get(base.String() + "/.well-known/agent-card.json")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return base, stop
			}
		}
		if command.ProcessState != nil && command.ProcessState.Exited() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	t.Fatal("official local echo agent did not become ready")
	return nil, func() {}
}

func startTrustedAgentCardProxy(t *testing.T, echoURL *url.URL) (string, []string) {
	t.Helper()
	proxy := httputil.NewSingleHostReverseProxy(echoURL)
	server := httptest.NewTLSServer(proxy)
	t.Cleanup(server.Close)
	certificate := server.Certificate()
	if certificate == nil {
		t.Fatal("Agent Card proxy has no certificate")
	}
	if _, err := x509.ParseCertificate(certificate.Raw); err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(t.TempDir(), "agent-card-ca.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	if err := os.WriteFile(certificatePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return server.URL + "/.well-known/agent-card.json", []string{
		"SSL_CERT_FILE=" + certificatePath,
	}
}

func jsonContainsString(encoded []byte, target string) bool {
	var value any
	if json.Unmarshal(encoded, &value) != nil {
		return false
	}
	var contains func(any) bool
	contains = func(current any) bool {
		switch typed := current.(type) {
		case string:
			return typed == target
		case []any:
			for _, child := range typed {
				if contains(child) {
					return true
				}
			}
		case map[string]any:
			for _, child := range typed {
				if contains(child) {
					return true
				}
			}
		}
		return false
	}
	return contains(value) && !strings.Contains(string(encoded), "qualification failure")
}
