package a2adispatch

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestForwardedArguments(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		want      []string
		handled   bool
	}{
		{name: "namespace", arguments: []string{"kado", "a2a"}, want: []string{}, handled: true},
		{
			name:      "all sidecar values",
			arguments: []string{"kado", "a2a", "send", "", "héllo world", "--agent", "sidecar", "--version"},
			want:      []string{"send", "", "héllo world", "--agent", "sidecar", "--version"},
			handled:   true,
		},
		{
			name:      "leading agent",
			arguments: []string{"kado", "--agent", "codex", "a2a", "future", "--flag", "value"},
			want:      []string{"future", "--flag", "value"},
			handled:   true,
		},
		{
			name:      "leading agent equals",
			arguments: []string{"kado", "--agent=codex", "a2a", "--help"},
			want:      []string{"--help"},
			handled:   true,
		},
		{name: "root help alias", arguments: []string{"kado", "help", "a2a"}, want: []string{"--help"}, handled: true},
		{
			name:      "nested help alias",
			arguments: []string{"kado", "--agent", "codex", "help", "a2a", "task", "get"},
			want:      []string{"help", "task", "get"},
			handled:   true,
		},
		{name: "different namespace", arguments: []string{"kado", "use", "send", "hello"}},
		{name: "misplaced namespace", arguments: []string{"kado", "help", "search", "a2a"}},
		{name: "bare help", arguments: []string{"kado", "help"}},
		{name: "missing command", arguments: []string{"kado"}},
		{name: "incomplete agent", arguments: []string{"kado", "--agent"}},
		{name: "empty agent", arguments: []string{"kado", "--agent", "", "a2a"}},
		{name: "empty agent equals", arguments: []string{"kado", "--agent=", "a2a"}},
		{name: "agent without command", arguments: []string{"kado", "--agent", "codex"}},
		{name: "duplicate agent", arguments: []string{"kado", "--agent", "codex", "--agent=default", "a2a"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, handled := forwardedArguments(test.arguments)
			if handled != test.handled || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("forwardedArguments(%q) = (%q, %v), want (%q, %v)", test.arguments, got, handled, test.want, test.handled)
			}
		})
	}
}

func TestForwardedArgumentsDoesNotMaintainAnUpstreamPolicy(t *testing.T) {
	invocations := [][]string{
		{"--agent-card", "https://agent.example/card.json", "card", "get"},
		{"--output", "json", "send", "hello"},
		{"send", "--async", "hello"},
		{"send", "--stream", "hello"},
		{"task", "subscribe", "task-1"},
		{"server", "--exec", "printf hello"},
		{"--endpoint", "https://agent.example/a2a", "--transport", "grpc", "send", "hello"},
		{"future-upstream-command", "--new-flag", "value"},
	}
	for _, invocation := range invocations {
		arguments := append([]string{"kado", "a2a"}, invocation...)
		got, handled := forwardedArguments(arguments)
		if !handled || !reflect.DeepEqual(got, invocation) {
			t.Fatalf("forwardedArguments(%q) = (%q, %v), want (%q, true)", arguments, got, handled, invocation)
		}
	}
}

func TestMatchesUsesTheDispatchBoundary(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		matches   bool
	}{
		{arguments: []string{"kado", "a2a", "send", "hello"}, matches: true},
		{arguments: []string{"kado", "--agent", "codex", "a2a", "--help"}, matches: true},
		{arguments: []string{"kado", "help", "a2a"}, matches: true},
		{arguments: []string{"kado", "--agent=codex", "help", "a2a", "task"}, matches: true},
		{arguments: []string{"kado", "__complete", "a2a", "send", "--"}, matches: true},
		{arguments: []string{"kado", "__completeNoDesc", "--agent", "codex", "a2a", ""}, matches: true},
		{arguments: []string{"kado", "help"}},
		{arguments: []string{"kado", "use", "send", "hello"}},
		{arguments: []string{"kado", "search", "a2a"}},
		{arguments: []string{"kado", "__complete", "search", "a2a"}},
		{arguments: []string{"kado", "--agent"}},
	} {
		if got := Matches(test.arguments); got != test.matches {
			t.Fatalf("Matches(%q) = %v, want %v", test.arguments, got, test.matches)
		}
	}
}

func TestCompletionDispatchStripsOnlyKadoOwnedTokens(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      []string
		handled   bool
	}{
		{
			arguments: []string{"kado", "__complete", "a2a", "send", "--"},
			want:      []string{"__complete", "send", "--"},
			handled:   true,
		},
		{
			arguments: []string{"kado", "__completeNoDesc", "--agent", "codex", "a2a", ""},
			want:      []string{"__completeNoDesc", ""},
			handled:   true,
		},
		{
			arguments: []string{"kado", "__complete", "a2a", "future-command", "--future-flag", "value"},
			want:      []string{"__complete", "future-command", "--future-flag", "value"},
			handled:   true,
		},
		{arguments: []string{"kado", "__complete", "search", "a2a"}},
	} {
		request, handled := dispatchRequestFor(test.arguments)
		if handled != test.handled || !reflect.DeepEqual(request.arguments, test.want) || request.completion != test.handled {
			t.Fatalf("dispatchRequestFor(%q) = (%q, completion=%v, handled=%v), want (%q, %v)", test.arguments, request.arguments, request.completion, handled, test.want, test.handled)
		}
	}
}

func TestCompletionFailureIsQuietCobraProtocol(t *testing.T) {
	var stdout, stderr strings.Builder
	code, handled := failure(true, &stdout, &stderr, "must not be shown")
	if code != 0 || !handled || stdout.String() != ":1\n" || stderr.Len() != 0 {
		t.Fatalf("code=%d handled=%v stdout=%q stderr=%q", code, handled, stdout.String(), stderr.String())
	}
}

func TestVerifiedSidecarUsesCanonicalExecutableDirectory(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "installed bundle ü")
	public := filepath.Join(root, "public bin")
	for _, path := range []string{bundle, public} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(bundle, kadoExecutableName())
	if err := os.WriteFile(executable, []byte("kado"), 0o755); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(bundle, sidecarName())
	value := []byte("version-matched-sidecar")
	if err := os.WriteFile(sidecar, value, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(value))
	link := filepath.Join(public, kadoExecutableName())
	if err := os.Symlink(executable, link); err != nil {
		t.Skipf("executable symlinks are unavailable: %v", err)
	}
	got, err := verifiedSidecar(link, int64(len(value)), digest)
	if err != nil || !sameFilePath(got, sidecar) {
		t.Fatalf("verifiedSidecar() = %q, %v; want %q", got, err, sidecar)
	}
}

func TestVerifiedSidecarFailsClosed(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, kadoExecutableName())
	if err := os.WriteFile(executable, []byte("kado"), 0o755); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(root, sidecarName())
	original := []byte("expected-sidecar")
	digest := fmt.Sprintf("%x", sha256.Sum256(original))
	write := func(value []byte) {
		t.Helper()
		_ = os.RemoveAll(sidecar)
		if err := os.WriteFile(sidecar, value, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	write(original)
	if _, err := verifiedSidecar(executable, int64(len(original)), digest); err != nil {
		t.Fatalf("valid sidecar rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		size   int64
		digest string
		value  []byte
	}{
		{name: "missing size", digest: digest, value: original},
		{name: "oversized metadata", size: maxSidecarSize + 1, digest: digest, value: original},
		{name: "invalid digest", size: int64(len(original)), digest: "unknown", value: original},
		{name: "uppercase digest", size: int64(len(original)), digest: fmt.Sprintf("%X", sha256.Sum256(original)), value: original},
		{name: "different size", size: int64(len(original)), digest: digest, value: append(append([]byte{}, original...), '!')},
		{name: "same size tamper", size: int64(len(original)), digest: digest, value: []byte("tampered-sidecar")},
	} {
		t.Run(test.name, func(t *testing.T) {
			write(test.value)
			if _, err := verifiedSidecar(executable, test.size, test.digest); err == nil {
				t.Fatal("invalid sidecar was accepted")
			}
		})
	}

	t.Run("missing", func(t *testing.T) {
		_ = os.RemoveAll(sidecar)
		if _, err := verifiedSidecar(executable, int64(len(original)), digest); err == nil {
			t.Fatal("missing sidecar was accepted")
		}
	})
	t.Run("nonregular", func(t *testing.T) {
		_ = os.RemoveAll(sidecar)
		if err := os.Mkdir(sidecar, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := verifiedSidecar(executable, int64(len(original)), digest); err == nil {
			t.Fatal("non-regular sidecar was accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		_ = os.RemoveAll(sidecar)
		target := filepath.Join(root, "other-sidecar")
		if err := os.WriteFile(target, original, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, sidecar); err != nil {
			t.Skipf("sidecar symlinks are unavailable: %v", err)
		}
		if _, err := verifiedSidecar(executable, int64(len(original)), digest); err == nil {
			t.Fatal("symlink sidecar was accepted")
		}
	})
}

func kadoExecutableName() string {
	if runtime.GOOS == "windows" {
		return "kado.exe"
	}
	return "kado"
}

func sameFilePath(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
