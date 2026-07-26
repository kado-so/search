// Package requestmeta attaches best-effort, unverified client metadata to Kado
// requests. None of these values participate in authentication or identity.
package requestmeta

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kado-so/search/internal/buildinfo"
)

const (
	HeaderClientName       = "Kado-Client-Name"
	HeaderClientVersion    = "Kado-Client-Version"
	HeaderClientPlatform   = "Kado-Client-Platform"
	HeaderAgentRuntime     = "Kado-Agent-Runtime"
	HeaderRuntimeDetection = "Kado-Agent-Runtime-Detection"
	HeaderInstallation     = "Kado-Installation-Metadata"
)

const (
	clientName        = "kado-cli"
	unknown           = "unknown"
	maxLocalValueSize = 254
)

type runtimeMetadata struct {
	name      string
	detection string
}

type installationMetadata struct {
	Hostname      string `json:"hostname,omitempty"`
	LocalUsername string `json:"local_username,omitempty"`
	LocalEmail    string `json:"local_email,omitempty"`
}

// Transport adds metadata to a cloned request before delegating to Base.
type Transport struct {
	Base                 http.RoundTripper
	clientVersion        string
	clientPlatform       string
	agentRuntime         string
	runtimeDetection     string
	installationMetadata string
}

// NewTransport detects the local execution environment once. The detected
// runtime is then attached to every request made through the transport.
func NewTransport(base http.RoundTripper, info buildinfo.Info) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	detected := detectRuntime(os.LookupEnv, processAncestry())
	installation, _ := json.Marshal(installationMetadata{
		Hostname:      boundedLocalValue(hostname()),
		LocalUsername: boundedLocalValue(localUsername()),
		LocalEmail:    boundedLocalValue(localEmail()),
	})
	return &Transport{
		Base:                 base,
		clientVersion:        token(info.Version),
		clientPlatform:       runtime.GOOS + "/" + runtime.GOARCH,
		agentRuntime:         detected.name,
		runtimeDetection:     detected.detection,
		installationMetadata: base64.RawURLEncoding.EncodeToString(installation),
	}
}

func (transport *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	cloned.Header.Set(HeaderClientName, clientName)
	cloned.Header.Set(HeaderClientVersion, transport.clientVersion)
	cloned.Header.Set(HeaderClientPlatform, transport.clientPlatform)
	cloned.Header.Set(HeaderAgentRuntime, transport.agentRuntime)
	cloned.Header.Set(HeaderRuntimeDetection, transport.runtimeDetection)
	cloned.Header.Set(
		"User-Agent",
		clientName+"/"+transport.clientVersion+" ("+transport.clientPlatform+")",
	)
	if strings.HasSuffix(cloned.URL.Path, "/api/auth/agent/enroll") {
		cloned.Header.Set(HeaderInstallation, transport.installationMetadata)
	} else {
		cloned.Header.Del(HeaderInstallation)
	}
	return transport.Base.RoundTrip(cloned)
}

type environmentLookup func(string) (string, bool)

func detectRuntime(environment environmentLookup, processes []string) runtimeMetadata {
	indicators := []struct {
		name string
		keys []string
	}{
		{name: "codex", keys: []string{"CODEX_THREAD_ID", "CODEX_CI", "CODEX_SHELL"}},
		{name: "claude-code", keys: []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"}},
		{name: "cursor", keys: []string{"CURSOR_TRACE_ID", "CURSOR_AGENT"}},
		{name: "gemini-cli", keys: []string{"GEMINI_CLI", "GEMINI_CLI_IDE_SERVER_PORT"}},
		{name: "opencode", keys: []string{"OPENCODE", "OPENCODE_CLIENT"}},
		{name: "github-copilot", keys: []string{"COPILOT_AGENT_SESSION", "GH_COPILOT"}},
		{name: "openhands", keys: []string{"OPENHANDS_RUNTIME", "OPENHANDS_SESSION_ID"}},
		{name: "devin", keys: []string{"DEVIN_SESSION_ID"}},
		{name: "amp", keys: []string{"AMP_THREAD_ID"}},
		{name: "hermes", keys: []string{"HERMES_SESSION_ID"}},
	}
	for _, indicator := range indicators {
		for _, key := range indicator.keys {
			if value, exists := environment(key); exists && strings.TrimSpace(value) != "" {
				return runtimeMetadata{name: indicator.name, detection: "environment"}
			}
		}
	}

	processIndicators := []struct {
		name    string
		matches []string
	}{
		{name: "claude-code", matches: []string{"claude", "claude-code"}},
		{name: "gemini-cli", matches: []string{"gemini", "gemini-cli"}},
		{name: "github-copilot", matches: []string{"copilot", "github-copilot"}},
		{name: "openhands", matches: []string{"openhands"}},
		{name: "opencode", matches: []string{"opencode"}},
		{name: "cursor", matches: []string{"cursor", "cursor-agent"}},
		{name: "codex", matches: []string{"codex"}},
		{name: "aider", matches: []string{"aider"}},
		{name: "goose", matches: []string{"goose"}},
		{name: "devin", matches: []string{"devin"}},
		{name: "hermes", matches: []string{"hermes"}},
		{name: "amp", matches: []string{"amp"}},
	}
	for _, process := range processes {
		normalized := strings.ToLower(process)
		for _, indicator := range processIndicators {
			for _, match := range indicator.matches {
				if processNameMatches(normalized, match) {
					return runtimeMetadata{name: indicator.name, detection: "process_tree"}
				}
			}
		}
	}
	return runtimeMetadata{name: unknown, detection: "none"}
}

func processNameMatches(process, expected string) bool {
	fields := strings.FieldsFunc(process, func(character rune) bool {
		return character == '/' || character == '\\' || character == ' ' || character == '(' ||
			character == ')' || character == '.'
	})
	for _, field := range fields {
		if field == expected || strings.HasPrefix(field, expected+"-") {
			return true
		}
	}
	return false
}

func processAncestry() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil
	}
	type process struct {
		parent int
		name   string
	}
	processes := make(map[int]process)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		id, idErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if idErr != nil || parentErr != nil {
			continue
		}
		processes[id] = process{parent: parent, name: strings.Join(fields[2:], " ")}
	}
	ancestry := make([]string, 0, 8)
	seen := make(map[int]bool)
	for id := os.Getppid(); id > 0 && len(ancestry) < 16 && !seen[id]; {
		seen[id] = true
		process, exists := processes[id]
		if !exists {
			break
		}
		ancestry = append(ancestry, process.name)
		id = process.parent
	}
	return ancestry
}

func hostname() string {
	value, _ := os.Hostname()
	return value
}

func localUsername() string {
	current, err := user.Current()
	if err == nil && current.Username != "" {
		return current.Username
	}
	for _, key := range []string{"USER", "USERNAME"} {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func localEmail() string {
	if value := boundedEmail(os.Getenv("EMAIL")); value != "" {
		return value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "config", "--global", "--get", "user.email").Output()
	if err != nil {
		return ""
	}
	return boundedEmail(string(output))
}

func boundedEmail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxLocalValueSize ||
		!strings.Contains(value, "@") ||
		strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func boundedLocalValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxLocalValueSize || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func token(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return unknown
	}
	output := strings.Builder{}
	for _, character := range value {
		if output.Len() == 64 {
			break
		}
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("._~-", character) {
			output.WriteRune(character)
		}
	}
	if output.Len() == 0 {
		return unknown
	}
	return output.String()
}
