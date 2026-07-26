package requestmeta

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/buildinfo"
)

func TestDetectRuntime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		env       map[string]string
		processes []string
		want      runtimeMetadata
	}{
		{
			name: "codex environment",
			env:  map[string]string{"CODEX_THREAD_ID": "thread"},
			want: runtimeMetadata{name: "codex", detection: "environment"},
		},
		{
			name:      "claude process tree",
			processes: []string{"/bin/zsh", "/usr/local/bin/claude"},
			want:      runtimeMetadata{name: "claude-code", detection: "process_tree"},
		},
		{
			name: "environment takes precedence",
			env:  map[string]string{"CURSOR_TRACE_ID": "trace"},
			processes: []string{
				"/Applications/Codex.app/Contents/Resources/codex",
			},
			want: runtimeMetadata{name: "cursor", detection: "environment"},
		},
		{
			name: "unknown",
			want: runtimeMetadata{name: "unknown", detection: "none"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lookup := func(key string) (string, bool) {
				value, exists := test.env[key]
				return value, exists
			}
			if got := detectRuntime(lookup, test.processes); got != test.want {
				t.Fatalf("detectRuntime() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestTransportAttachesRequestAndEnrollmentMetadata(t *testing.T) {
	t.Parallel()
	capture := &captureTransport{}
	transport := &Transport{
		Base:                 capture,
		clientVersion:        "0.4.0",
		clientPlatform:       "darwin/arm64",
		agentRuntime:         "codex",
		runtimeDetection:     "environment",
		installationMetadata: "eyJob3N0bmFtZSI6Im1hYyJ9",
	}

	request, err := http.NewRequest(
		http.MethodPost,
		"https://kado.so/api/auth/agent/enroll",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(HeaderAgentRuntime, "caller-supplied")
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if got := capture.request.Header.Get(HeaderAgentRuntime); got != "codex" {
		t.Fatalf("%s = %q", HeaderAgentRuntime, got)
	}
	if got := capture.request.Header.Get(HeaderInstallation); got == "" {
		t.Fatalf("%s is missing", HeaderInstallation)
	}
	if got := capture.request.Header.Get("User-Agent"); got != "kado-cli/0.4.0 (darwin/arm64)" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := request.Header.Get(HeaderAgentRuntime); got != "caller-supplied" {
		t.Fatalf("original request was mutated: %q", got)
	}

	search, err := http.NewRequest(http.MethodGet, "https://kado.so/search?q=test", nil)
	if err != nil {
		t.Fatal(err)
	}
	search.Header.Set(HeaderInstallation, "should-be-removed")
	if _, err := transport.RoundTrip(search); err != nil {
		t.Fatal(err)
	}
	if got := capture.request.Header.Get(HeaderInstallation); got != "" {
		t.Fatalf("Search leaked installation metadata: %q", got)
	}
}

func TestNewTransportProducesBoundedMetadata(t *testing.T) {
	t.Parallel()
	transport := NewTransport(&captureTransport{}, buildinfo.Info{Version: "dev build"})
	if transport.clientVersion != "devbuild" {
		t.Fatalf("client version = %q", transport.clientVersion)
	}
	if transport.clientPlatform == "" ||
		transport.agentRuntime == "" ||
		transport.runtimeDetection == "" ||
		transport.installationMetadata == "" {
		t.Fatalf("incomplete metadata: %#v", transport)
	}
}

type captureTransport struct {
	request *http.Request
}

func (transport *captureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.request = request
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    request,
	}, nil
}
