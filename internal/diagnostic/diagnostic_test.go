package diagnostic

import (
	"errors"
	"strings"
	"testing"
)

func TestPublicRendersOnlySafeDiagnostic(t *testing.T) {
	t.Parallel()

	private := errors.New("private token: should-not-render")
	err := New(
		"invalid_input\nspoofed",
		strings.Repeat("x", 300)+"\nspoofed",
		ExitUsage,
		private,
	)

	code, message, exitCode := Public(err)
	if strings.Contains(code, "\n") || strings.Contains(message, "\n") {
		t.Fatalf("diagnostic contains line break: code=%q message=%q", code, message)
	}
	if strings.Contains(message, private.Error()) {
		t.Fatalf("diagnostic exposed private cause: %q", message)
	}
	if len(message) > maxMessageBytes {
		t.Fatalf("message is not bounded: %d bytes", len(message))
	}
	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
}

func TestPublicRedactsUnknownErrors(t *testing.T) {
	t.Parallel()

	code, message, exitCode := Public(errors.New("access_token=secret"))
	if code != fallbackCode || message != fallbackMessage || exitCode != ExitFailure {
		t.Fatalf("Public() = %q, %q, %d", code, message, exitCode)
	}
}
