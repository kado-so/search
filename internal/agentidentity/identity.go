// Package agentidentity identifies the local calling agent without exposing
// raw process or environment evidence.
package agentidentity

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const Default = "default"

var known = []string{
	"aider",
	"amp",
	"antigravity",
	"claude-code",
	"codex",
	"cursor",
	"default",
	"devin",
	"gemini-cli",
	"github-copilot",
	"goose",
	"hermes",
	"opencode",
	"openhands",
}

type Detection struct {
	Agent  string
	Source string
}

func Known() []string {
	return append([]string(nil), known...)
}

func Valid(value string) bool {
	index := sort.SearchStrings(known, value)
	return index < len(known) && known[index] == value
}

func Detect(override string) (Detection, error) {
	return detect(override, os.LookupEnv, processAncestry())
}

type environmentLookup func(string) (string, bool)

func detect(
	override string,
	environment environmentLookup,
	processes []string,
) (Detection, error) {
	if override != "" {
		if !Valid(override) {
			return Detection{}, errors.New("unknown agent identity")
		}
		return Detection{Agent: override, Source: "override"}, nil
	}
	for _, process := range processes {
		if agent := agentForProcess(process); agent != "" {
			return Detection{Agent: agent, Source: "process"}, nil
		}
	}
	if agent := agentFromEnvironment(environment); agent != "" {
		return Detection{Agent: agent, Source: "environment"}, nil
	}
	return Detection{Agent: Default, Source: "default"}, nil
}

func agentFromEnvironment(environment environmentLookup) string {
	indicators := []struct {
		agent string
		keys  []string
	}{
		{agent: "codex", keys: []string{"CODEX_THREAD_ID", "CODEX_CI", "CODEX_SHELL"}},
		{agent: "claude-code", keys: []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"}},
		{agent: "cursor", keys: []string{"CURSOR_TRACE_ID", "CURSOR_AGENT"}},
		{agent: "gemini-cli", keys: []string{"GEMINI_CLI", "GEMINI_CLI_IDE_SERVER_PORT"}},
		{agent: "opencode", keys: []string{"OPENCODE", "OPENCODE_CLIENT"}},
		{agent: "github-copilot", keys: []string{"COPILOT_AGENT_SESSION", "GH_COPILOT"}},
		{agent: "openhands", keys: []string{"OPENHANDS_RUNTIME", "OPENHANDS_SESSION_ID"}},
		{agent: "devin", keys: []string{"DEVIN_SESSION_ID"}},
		{agent: "amp", keys: []string{"AMP_THREAD_ID"}},
		{agent: "hermes", keys: []string{"HERMES_SESSION_ID"}},
	}
	found := ""
	for _, indicator := range indicators {
		for _, key := range indicator.keys {
			if value, exists := environment(key); exists && strings.TrimSpace(value) != "" {
				if found != "" && found != indicator.agent {
					return ""
				}
				found = indicator.agent
				break
			}
		}
	}
	return found
}

func agentForProcess(value string) string {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(value)))
	name = strings.TrimSuffix(name, ".exe")
	if strings.HasPrefix(name, "antigravity helper") {
		return "antigravity"
	}
	matches := []struct {
		agent string
		names []string
	}{
		{agent: "claude-code", names: []string{"claude", "claude-code"}},
		{agent: "antigravity", names: []string{"agy", "antigravity", "antigravity-cli"}},
		{agent: "gemini-cli", names: []string{"gemini", "gemini-cli"}},
		{agent: "github-copilot", names: []string{"copilot", "github-copilot"}},
		{agent: "openhands", names: []string{"openhands"}},
		{agent: "opencode", names: []string{"opencode"}},
		{agent: "cursor", names: []string{"cursor", "cursor-agent"}},
		{agent: "codex", names: []string{"codex"}},
		{agent: "aider", names: []string{"aider"}},
		{agent: "goose", names: []string{"goose"}},
		{agent: "devin", names: []string{"devin"}},
		{agent: "hermes", names: []string{"hermes"}},
		{agent: "amp", names: []string{"amp"}},
	}
	for _, match := range matches {
		for _, expected := range match.names {
			if name == expected || strings.HasPrefix(name, expected+"-") {
				return match.agent
			}
		}
	}
	return ""
}
