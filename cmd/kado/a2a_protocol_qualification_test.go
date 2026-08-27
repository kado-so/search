package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestA2ACandidateTransportCardAndTurnQualification(t *testing.T) {
	kado, sidecar := qualificationPair(t)
	tests := []struct {
		name      string
		transport string
		protocol  string
		insecure  bool
	}{
		{name: "rest-v1", transport: "rest", protocol: "latest"},
		{name: "jsonrpc-v1", transport: "jsonrpc", protocol: "latest"},
		{name: "grpc-v1", transport: "grpc", protocol: "latest", insecure: true},
		{name: "rest-v0.3-compatible-card", transport: "rest", protocol: "0.3"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			cardURL, stop := startCandidateEchoAgent(t, kado, test.transport, test.protocol)
			t.Cleanup(stop)
			message := "transport qualification " + test.name
			arguments := []string{"a2a", "--agent-card", cardURL, "--output", "json"}
			if test.insecure {
				arguments = append(arguments, "--insecure")
			}
			arguments = append(arguments, "send", message)
			result := runA2ATestProcess(t, kado, sanitizedA2AEnvironment(), "", arguments...)
			if test.protocol == "0.3" {
				directArguments := append([]string(nil), arguments[1:]...)
				direct := runA2ATestProcess(t, sidecar, sanitizedA2AEnvironment(), "", directArguments...)
				if direct != result || result.exitCode == 0 || !strings.Contains(result.stderr, "server error") {
					t.Fatalf("v0.3 pinned behavior direct=%+v delegated=%+v", direct, result)
				}
				return
			}
			if result.exitCode != 0 || !jsonContainsString([]byte(result.stdout), message) {
				t.Fatalf("transport result exit=%d stderr=%q stdout=%q", result.exitCode, result.stderr, result.stdout)
			}
			if test.transport == "grpc" {
				withoutInsecure := runA2ATestProcess(
					t, kado, sanitizedA2AEnvironment(), "",
					"a2a", "--agent-card", cardURL, "--output", "json", "send", "secure gRPC qualification",
				)
				if withoutInsecure.exitCode == 0 || !strings.Contains(withoutInsecure.stderr, "--insecure") {
					t.Fatalf("plaintext gRPC without --insecure = %+v", withoutInsecure)
				}
			}

			if test.name != "rest-v1" {
				return
			}
			taskID, contextID := taskAndContextID([]byte(result.stdout))
			if taskID == "" || contextID == "" {
				t.Fatalf("send response omitted task/context identity: %s", result.stdout)
			}
			get := runA2ATestProcess(
				t, kado, sanitizedA2AEnvironment(), "",
				"a2a", "--agent-card", cardURL, "--output", "json", "task", "get", taskID,
			)
			if get.exitCode != 0 || !jsonContainsString([]byte(get.stdout), taskID) {
				t.Fatalf("task get exit=%d stderr=%q stdout=%q", get.exitCode, get.stderr, get.stdout)
			}
			taskFollowup := runA2ATestProcess(
				t, kado, sanitizedA2AEnvironment(), "",
				"a2a", "--agent-card", cardURL, "--output", "json",
				"send", "--task-id", taskID, "task continuation qualification",
			)
			if taskFollowup.exitCode == 0 {
				continuedID, _ := taskAndContextID([]byte(taskFollowup.stdout))
				if continuedID != taskID {
					t.Fatalf("task continuation silently created task %q instead of %q", continuedID, taskID)
				}
			} else if taskFollowup.stderr == "" {
				t.Fatal("terminal task continuation failed without an upstream diagnostic")
			}
			followupMessage := "context continuation qualification"
			followup := runA2ATestProcess(
				t, kado, sanitizedA2AEnvironment(), "",
				"a2a", "--agent-card", cardURL, "--output", "json",
				"send", "--context-id", contextID, followupMessage,
			)
			if followup.exitCode != 0 || !jsonContainsString([]byte(followup.stdout), followupMessage) {
				t.Fatalf("context continuation exit=%d stderr=%q stdout=%q", followup.exitCode, followup.stderr, followup.stdout)
			}
			continuedTask, continuedContext := taskAndContextID([]byte(followup.stdout))
			if continuedTask == "" || continuedTask == taskID || continuedContext != contextID {
				t.Fatalf("context continuation identities task=%q context=%q", continuedTask, continuedContext)
			}
		})
	}
}

func TestA2ACandidateExplicitCredentialBoundary(t *testing.T) {
	kado, _ := qualificationPair(t)
	for _, test := range []struct {
		name             string
		flags            []string
		wantCardAuth     bool
		wantEndpointAuth bool
	}{
		{name: "no injection"},
		{
			name:             "auth shorthand",
			flags:            []string{"--auth", "Bearer qualification-fixture"},
			wantCardAuth:     true,
			wantEndpointAuth: true,
		},
		{
			name:             "service parameter",
			flags:            []string{"--svc-param", "Authorization=Bearer qualification-fixture"},
			wantEndpointAuth: true,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			cardURL, cardAuth, endpointAuth, closeServers := credentialCaptureAgent(t)
			t.Cleanup(closeServers)
			arguments := []string{"a2a", "--agent-card", cardURL, "--output", "json"}
			arguments = append(arguments, test.flags...)
			arguments = append(arguments, "send", "credential boundary qualification")
			result := runA2ATestProcess(t, kado, sanitizedA2AEnvironment(), "", arguments...)
			if result.exitCode == 0 {
				t.Fatal("capture endpoint intentionally failed but candidate returned success")
			}
			if (capturedHeader(t, cardAuth) != "") != test.wantCardAuth ||
				(capturedHeader(t, endpointAuth) != "") != test.wantEndpointAuth {
				t.Fatal("credential propagation did not match the reviewed official boundary")
			}
		})
	}
}

func TestA2ACandidateRejectsUntrustedAgentCardTLS(t *testing.T) {
	kado, _ := qualificationPair(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{}`)
	}))
	defer server.Close()
	result := runA2ATestProcess(
		t, kado, sanitizedA2AEnvironment(), "",
		"a2a", "--agent-card", server.URL+"/.well-known/agent-card.json", "send", "tls qualification",
	)
	if result.exitCode == 0 ||
		(!strings.Contains(result.stderr, "certificate") && !strings.Contains(result.stderr, "x509")) {
		t.Fatalf("untrusted Agent Card TLS result exit=%d stderr=%q", result.exitCode, result.stderr)
	}
}

func startCandidateEchoAgent(t *testing.T, binary, transport, protocol string) (string, func()) {
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
	arguments := []string{
		"a2a", "--output", "json", "server", "--echo",
		"--host", "127.0.0.1", "--port", port,
		"--serve-transport", transport, "--protocol", protocol,
	}
	if protocol == "0.3" {
		arguments = append(arguments, "--card-compat")
	}
	command := exec.Command(binary, arguments...)
	command.Stdout = io.Discard
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if command.Process != nil {
				_ = command.Process.Kill()
				_, _ = command.Process.Wait()
			}
		})
	}

	cardURL := "http://" + address + "/.well-known/agent-card.json"
	if transport == "grpc" {
		lines := make(chan string, 4)
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				lines <- scanner.Text()
			}
			close(lines)
		}()
		deadline := time.After(15 * time.Second)
		for cardURL == "http://"+address+"/.well-known/agent-card.json" {
			select {
			case line, open := <-lines:
				if !open {
					stop()
					t.Fatal("gRPC echo agent exited before advertising its Agent Card")
				}
				if strings.HasPrefix(line, "Agent card at ") {
					cardURL = connectableLoopbackURL(t, strings.TrimPrefix(line, "Agent card at "))
				}
			case <-deadline:
				stop()
				t.Fatal("gRPC echo agent did not advertise its Agent Card")
			}
		}
	} else {
		go func() { _, _ = io.Copy(io.Discard, stderr) }()
	}
	waitForAgentCard(t, cardURL, stop)
	return cardURL, stop
}

func waitForAgentCard(t *testing.T, cardURL string, stop func()) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(cardURL)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	t.Fatal("official local echo Agent Card did not become ready")
}

func connectableLoopbackURL(t *testing.T, value string) string {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = net.JoinHostPort("127.0.0.1", port)
	return parsed.String()
}

func taskAndContextID(encoded []byte) (string, string) {
	var value map[string]any
	if json.Unmarshal(encoded, &value) != nil {
		return "", ""
	}
	task := value
	if wrapped, ok := value["task"].(map[string]any); ok {
		task = wrapped
	}
	id, _ := task["id"].(string)
	contextID, _ := task["contextId"].(string)
	return id, contextID
}

func credentialCaptureAgent(t *testing.T) (string, <-chan string, <-chan string, func()) {
	t.Helper()
	endpointAuth := make(chan string, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		endpointAuth <- request.Header.Get("Authorization")
		http.Error(response, "intentional qualification failure", http.StatusInternalServerError)
	}))
	cardAuth := make(chan string, 1)
	card := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cardAuth <- request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"protocolVersion": "1.0",
			"name":            "Credential boundary agent",
			"description":     "Qualification fixture",
			"version":         "1.0.0",
			"supportedInterfaces": []map[string]string{
				{"url": endpoint.URL, "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0"},
			},
			"capabilities":       map[string]bool{},
			"defaultInputModes":  []string{"text/plain"},
			"defaultOutputModes": []string{"text/plain"},
			"skills":             []any{},
		})
	}))
	return card.URL + "/.well-known/agent-card.json", cardAuth, endpointAuth, func() {
		card.Close()
		endpoint.Close()
	}
}

func sanitizedA2AEnvironment() []string {
	blocked := []string{
		"A2ACLI_AGENT_CARD",
		"A2ACLI_AUTH",
		"A2ACLI_ENDPOINT",
		"A2ACLI_SVC_PARAM",
		"A2ACLI_TRANSPORT",
	}
	return environmentWithout(blocked...)
}

func capturedHeader(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("qualification server did not receive the expected request")
		return ""
	}
}
