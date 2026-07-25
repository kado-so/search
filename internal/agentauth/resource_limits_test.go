package agentauth

import (
	"net/http"
	"strings"
	"testing"
)

func TestAgentAuthAggregateAndHeaderLimits(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	if err := validateLimits(limits); err != nil {
		t.Fatalf("validateLimits(default) error = %v", err)
	}
	limits.MaxArgonMemoryKiB = 128 * 1024
	limits.MaxArgonPasses = 4
	if err := validateLimits(limits); err != nil {
		t.Fatalf("validateLimits(maximum aggregate) error = %v", err)
	}
	limits.MaxArgonPasses++
	if err := validateLimits(limits); err == nil {
		t.Fatal("validateLimits(excessive aggregate) error = nil")
	}

	headers := http.Header{"Content-Type": {"application/json"}}
	if !boundedResponseHeaders(headers, 128) {
		t.Fatal("boundedResponseHeaders(valid) = false")
	}
	headers.Set("X-Oversized", strings.Repeat("a", 129))
	if boundedResponseHeaders(headers, 128) {
		t.Fatal("boundedResponseHeaders(oversized) = true")
	}
}
