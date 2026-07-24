package diagnostic

import (
	"errors"
	"strings"
	"testing"
	"unicode"
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

func TestTerminalSafeTextRemovesTerminalControlsAndPreservesUnicode(t *testing.T) {
	t.Parallel()

	unsafe := "before\u001b\u0085\u009b\u2028\u2029\u202e\u2066after Café 世界 🧭"
	got := TerminalSafeText(unsafe, maxMessageBytes)
	want := "before after Café 世界 🧭"
	if got != want {
		t.Fatalf("TerminalSafeText() = %q, want %q", got, want)
	}
	if strings.ContainsFunc(got, unsafeTerminalRune) {
		t.Fatalf("TerminalSafeText() retained unsafe terminal rune: %q", got)
	}
}

func TestPublicDiagnosticRemovesC1BidiAndSeparatorControls(t *testing.T) {
	t.Parallel()

	err := New(
		"remote_failure",
		"safe\u0085C1\u009bformat\u202eright\u2028line\u2029paragraph",
		ExitFailure,
		nil,
	)
	code, message, exitCode := Public(err)
	if code != "remote_failure" ||
		message != "safe C1 format right line paragraph" ||
		exitCode != ExitFailure {
		t.Fatalf("Public() = %q, %q, %d", code, message, exitCode)
	}
	if strings.ContainsFunc(message, unsafeTerminalRune) {
		t.Fatalf("Public() retained unsafe terminal rune: %q", message)
	}
}

func unsafeTerminalRune(character rune) bool {
	return unicode.IsControl(character) ||
		unicode.In(character, unicode.Cf, unicode.Zl, unicode.Zp)
}
