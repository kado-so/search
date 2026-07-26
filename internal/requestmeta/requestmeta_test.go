package requestmeta

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type captureTransport struct {
	request *http.Request
}

func (capture *captureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	capture.request = request
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
		Request:    request,
	}, nil
}

func TestEnrollmentIncludesOnlyHostnameAndUsername(t *testing.T) {
	capture := &captureTransport{}
	transport := newTransport(capture, "workstation", "local-user")
	request, err := http.NewRequest(
		http.MethodPost,
		"https://kado.so/api/auth/agent/enroll",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	encoded := capture.request.Header.Get(HeaderInstallation)
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]string
	if err := json.Unmarshal(payload, &metadata); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"hostname":       "workstation",
		"local_username": "local-user",
	}
	if len(metadata) != len(want) {
		t.Fatalf("metadata = %#v", metadata)
	}
	for key, value := range want {
		if metadata[key] != value {
			t.Fatalf("metadata[%q] = %q, want %q", key, metadata[key], value)
		}
	}
}

func TestMetadataIsRemovedFromOtherRequests(t *testing.T) {
	capture := &captureTransport{}
	transport := newTransport(capture, "workstation", "local-user")
	request, err := http.NewRequest(http.MethodGet, "https://kado.so/search", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(HeaderInstallation, "stale")
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if value := capture.request.Header.Get(HeaderInstallation); value != "" {
		t.Fatalf("installation metadata leaked into Search: %q", value)
	}
}

func TestLocalValuesAreBoundedAndTerminalSafe(t *testing.T) {
	value := boundedLocalValue(" \n" + strings.Repeat("é", 200) + "\x00 ")
	if value == "" || len(value) > maxLocalValueSize || !strings.Contains(value, "é") {
		t.Fatalf("bounded value = %q", value)
	}
	for _, character := range value {
		if character < 0x20 {
			t.Fatalf("bounded value retained control character: %q", value)
		}
	}
}
