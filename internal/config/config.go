// Package config owns non-secret CLI configuration. Authentication credential
// storage is intentionally owned by a later, separate package.
package config

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/kado-so/search/internal/diagnostic"
)

const (
	EnvironmentBaseURL   = "KADO_BASE_URL"
	EnvironmentConfigDir = "KADO_CONFIG_DIR"
	defaultBaseURL       = "https://kado.so"
	configFileName       = "config.json"
)

type CredentialBackend string

const (
	CredentialBackendOS   CredentialBackend = "os"
	CredentialBackendFile CredentialBackend = "file"
)

// Config contains safe client configuration. It must never grow fields that
// hold private keys, assertions, or access tokens.
type Config struct {
	// BaseURL is an absolute HTTPS URL whose path is either "/" or a
	// canonical, unescaped ASCII path prefix without a trailing slash.
	// Callers may safely append endpoint path segments without first cleaning
	// or decoding attacker-controlled path data.
	BaseURL           *url.URL
	ConfigDir         string
	ConfigPath        string
	CredentialBackend CredentialBackend
	SecretsDir        string
}

// Load reads safe process configuration and platform configuration defaults.
func Load() (Config, error) {
	return load(os.LookupEnv, os.UserConfigDir)
}

type environmentLookup func(string) (string, bool)
type userConfigDirLookup func() (string, error)

func load(environment environmentLookup, userConfigDir userConfigDirLookup) (Config, error) {
	configDir, exists := environment(EnvironmentConfigDir)
	if !exists {
		root, lookupErr := userConfigDir()
		if lookupErr != nil {
			return Config{}, diagnostic.New(
				"config_directory_unavailable",
				"could not determine the user configuration directory",
				diagnostic.ExitFailure,
				lookupErr,
			)
		}
		configDir = filepath.Join(root, "kado")
	}
	configDir = strings.TrimSpace(configDir)
	if configDir == "" || !filepath.IsAbs(configDir) {
		return Config{}, diagnostic.New(
			"invalid_config_directory",
			"KADO_CONFIG_DIR must be an absolute path",
			diagnostic.ExitUsage,
			nil,
		)
	}
	configDir = filepath.Clean(configDir)
	configPath := filepath.Join(configDir, configFileName)
	file, err := readConfigFile(configPath)
	if err != nil {
		return Config{}, err
	}
	rawBaseURL := file.BaseURL
	if rawBaseURL == "" {
		rawBaseURL = defaultBaseURL
	}
	if configured, exists := environment(EnvironmentBaseURL); exists {
		rawBaseURL = configured
	}
	baseURL, err := parseBaseURL(rawBaseURL)
	if err != nil {
		return Config{}, err
	}
	backend := CredentialBackend(file.Credentials.Backend)
	if backend == "" {
		backend = CredentialBackendOS
	}
	if backend != CredentialBackendOS && backend != CredentialBackendFile {
		return Config{}, invalidConfigFile(nil)
	}
	secrets := file.Credentials.Directory
	if secrets == "" {
		secrets = "./secrets"
	}
	if secrets != strings.TrimSpace(secrets) ||
		containsControl(secrets) ||
		filepath.Clean(secrets) == "." {
		return Config{}, invalidConfigFile(nil)
	}
	if !filepath.IsAbs(secrets) {
		secrets = filepath.Join(configDir, secrets)
	}
	secrets = filepath.Clean(secrets)
	if !filepath.IsAbs(secrets) {
		return Config{}, invalidConfigFile(nil)
	}
	return Config{
		BaseURL:           baseURL,
		ConfigDir:         configDir,
		ConfigPath:        configPath,
		CredentialBackend: backend,
		SecretsDir:        secrets,
	}, nil
}

type fileConfig struct {
	BaseURL     string `json:"base_url,omitempty"`
	Credentials struct {
		Backend   string `json:"backend,omitempty"`
		Directory string `json:"directory,omitempty"`
	} `json:"credentials,omitempty"`
}

func readConfigFile(path string) (fileConfig, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileConfig{}, nil
	}
	if err != nil {
		return fileConfig{}, invalidConfigFile(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16*1024))
	decoder.DisallowUnknownFields()
	var configured fileConfig
	if err := decoder.Decode(&configured); err != nil {
		return fileConfig{}, invalidConfigFile(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fileConfig{}, invalidConfigFile(nil)
	}
	return configured, nil
}

func invalidConfigFile(cause error) error {
	return diagnostic.New(
		"invalid_config_file",
		"config.json contains invalid Kado configuration",
		diagnostic.ExitUsage,
		cause,
	)
}

func parseBaseURL(raw string) (*url.URL, error) {
	if raw == "" || raw != strings.TrimSpace(raw) || containsControl(raw) {
		return nil, invalidBaseURL(nil)
	}

	parsed, err := url.Parse(raw)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Hostname() == "" ||
		parsed.Opaque != "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" {
		return nil, invalidBaseURL(err)
	}

	if strings.HasSuffix(parsed.Host, ":") {
		return nil, invalidBaseURL(nil)
	}
	if port := parsed.Port(); port != "" {
		number, portErr := strconv.Atoi(port)
		if portErr != nil || number < 1 || number > 65_535 {
			return nil, invalidBaseURL(portErr)
		}
	}

	if err := validateBasePath(parsed); err != nil {
		return nil, err
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.RawPath = ""
	return parsed, nil
}

func validateBasePath(parsed *url.URL) error {
	path := parsed.Path
	if path == "" || path == "/" {
		return nil
	}
	if path[0] != '/' ||
		strings.HasSuffix(path, "/") ||
		parsed.RawPath != "" ||
		strings.Contains(parsed.EscapedPath(), "%") {
		return invalidBaseURL(nil)
	}

	for _, character := range path {
		if character == '/' {
			continue
		}
		if !isUnreservedASCII(character) {
			return invalidBaseURL(nil)
		}
	}

	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return invalidBaseURL(nil)
		}
	}
	return nil
}

func isUnreservedASCII(character rune) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' ||
		character == '-' ||
		character == '.' ||
		character == '_' ||
		character == '~'
}

func containsControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

func invalidBaseURL(cause error) error {
	return diagnostic.New(
		"invalid_base_url",
		"KADO_BASE_URL must be a canonical HTTPS URL with a valid port and optional unescaped base path",
		diagnostic.ExitUsage,
		cause,
	)
}
