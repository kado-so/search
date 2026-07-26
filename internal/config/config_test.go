package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/diagnostic"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	configRoot := filepath.Join(t.TempDir(), "config")
	got, err := load(noEnvironment, func() (string, error) {
		return configRoot, nil
	})
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if got.BaseURL.String() != "https://kado.so/" {
		t.Fatalf("BaseURL = %q", got.BaseURL)
	}
	wantDir := filepath.Join(configRoot, "kado")
	if got.ConfigDir != wantDir {
		t.Fatalf("ConfigDir = %q, want %q", got.ConfigDir, wantDir)
	}
	if got.CredentialBackend != CredentialBackendOS ||
		got.SecretsDir != filepath.Join(wantDir, "secrets") {
		t.Fatalf("default config = %#v", got)
	}
}

func TestLoadUsesConfigDirectoryOverride(t *testing.T) {
	t.Parallel()

	environment := map[string]string{
		EnvironmentConfigDir: filepath.Join(t.TempDir(), "kado"),
	}
	got, err := load(mapEnvironment(environment), unavailableConfigDir)
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if got.BaseURL.String() != "https://kado.so/" {
		t.Fatalf("BaseURL = %q", got.BaseURL)
	}
	if got.ConfigDir != environment[EnvironmentConfigDir] {
		t.Fatalf("ConfigDir = %q", got.ConfigDir)
	}
}

func TestLoadReadsFileCredentialConfiguration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kado")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "config.json"),
		[]byte(`{"base_url":"https://search.example.test","credentials":{"backend":"file","directory":"./private-secrets"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	got, err := load(
		mapEnvironment(map[string]string{EnvironmentConfigDir: root}),
		unavailableConfigDir,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL.String() != "https://search.example.test/" ||
		got.CredentialBackend != CredentialBackendFile ||
		got.SecretsDir != filepath.Join(root, "private-secrets") {
		t.Fatalf("file config = %#v", got)
	}
}

func TestLoadRejectsUnknownOrTrailingConfig(t *testing.T) {
	for _, encoded := range []string{
		`{"credentials":{"backend":"memory"}}`,
		`{"unknown":true}`,
		`{"credentials":{}} trailing`,
		`{"credentials":{"directory":"."}}`,
	} {
		root := filepath.Join(t.TempDir(), "kado")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(encoded), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := load(
			mapEnvironment(map[string]string{EnvironmentConfigDir: root}),
			unavailableConfigDir,
		); err == nil {
			t.Fatalf("load accepted %q", encoded)
		}
	}
}

func TestParseBaseURLAcceptsCanonicalHTTPSOriginsAndBasePaths(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "https://kado.so", want: "https://kado.so/"},
		{input: "https://kado.so/", want: "https://kado.so/"},
		{input: "https://kado.so:443", want: "https://kado.so:443/"},
		{
			input: "https://search.example.test:8443/base",
			want:  "https://search.example.test:8443/base",
		},
		{
			input: "https://search.example.test/base/v1.0/~agent_search",
			want:  "https://search.example.test/base/v1.0/~agent_search",
		},
		{input: "https://[2001:db8::1]:443/base", want: "https://[2001:db8::1]:443/base"},
	} {
		parsed, err := parseBaseURL(test.input)
		if err != nil {
			t.Errorf("parseBaseURL(%q) error = %v", test.input, err)
			continue
		}
		if parsed.Scheme != "https" || parsed.Hostname() == "" {
			t.Errorf("parseBaseURL(%q) = %q", test.input, parsed)
		}
		if parsed.Path != "/" && strings.HasSuffix(parsed.Path, "/") {
			t.Errorf("parseBaseURL(%q) retained trailing slash in %q", test.input, parsed.Path)
		}
		if parsed.String() != test.want {
			t.Errorf("parseBaseURL(%q) = %q, want %q", test.input, parsed, test.want)
		}
	}
}

func TestParseBaseURLRejectsAmbiguousUnsafeOrNonCanonicalTargets(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"http://kado.so",
		"https://user:secret@kado.so",
		"https://kado.so?access_token=secret",
		"https://kado.so?",
		"https://kado.so/#secret",
		"not a url",
		"https://kado.so:0",
		"https://kado.so:99999",
		"https://kado.so:65536",
		"https://kado.so:",
		"https://kado.so/base/",
		"https://kado.so/base//path",
		"https://kado.so/base/./path",
		"https://kado.so/base/../admin",
		"https://kado.so/../admin",
		"https://kado.so/base%2Fpath",
		"https://kado.so/base%2fpath",
		"https://kado.so/%2e%2e/admin",
		"https://kado.so/.%2e/admin",
		"https://kado.so/%62ase",
		"https://kado.so/base%00path",
		"https://kado.so/base%0apath",
		"https://kado.so/base path",
		"https://kado.so/base\\path",
		"https://kado.so/base\x00path",
		"https://kado.so/base\npath",
		" https://kado.so/base",
		"https://kado.so/base ",
	} {
		_, err := parseBaseURL(value)
		if err == nil {
			t.Errorf("parseBaseURL(%q) succeeded", value)
			continue
		}
		_, message, exitCode := diagnostic.Public(err)
		if strings.Contains(message, value) {
			t.Errorf("diagnostic echoed unsafe configuration %q: %q", value, message)
		}
		if exitCode != diagnostic.ExitUsage {
			t.Errorf("parseBaseURL(%q) exit code = %d, want %d", value, exitCode, diagnostic.ExitUsage)
		}
	}
}

func TestLoadRejectsRelativeOrUnavailableConfigDirectory(t *testing.T) {
	t.Parallel()

	_, err := load(
		mapEnvironment(map[string]string{EnvironmentConfigDir: "relative/path"}),
		unavailableConfigDir,
	)
	if err == nil {
		t.Fatal("load() with relative configuration directory succeeded")
	}

	_, err = load(noEnvironment, unavailableConfigDir)
	if err == nil {
		t.Fatal("load() with unavailable user configuration directory succeeded")
	}
}

func noEnvironment(string) (string, bool) {
	return "", false
}

func mapEnvironment(values map[string]string) environmentLookup {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}

func unavailableConfigDir() (string, error) {
	return "", errors.New("private operating system detail")
}
