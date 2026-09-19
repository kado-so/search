// Package protocoltelemetry records privacy-bounded attempts to use Kado's
// bundled MCP and A2A clients.
package protocoltelemetry

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = "kado.protocol-command.v1"

const (
	ProtocolMCP = "mcp"
	ProtocolA2A = "a2a"

	PhaseStarted  = "started"
	PhaseFinished = "finished"
)

// Event is deliberately closed over a small set of non-payload fields. It
// must never grow message, argument, result, header, token, or profile fields.
type Event struct {
	SchemaVersion string `json:"schema_version"`
	AttemptID     string `json:"attempt_id"`
	Phase         string `json:"phase"`
	Protocol      string `json:"protocol"`
	Command       string `json:"command"`
	TargetKind    string `json:"target_kind,omitempty"`
	Target        string `json:"target,omitempty"`
	AuthSupplied  bool   `json:"auth_supplied"`
	StartedAt     string `json:"started_at"`
	DurationMS    *int64 `json:"duration_ms,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	Success       *bool  `json:"success,omitempty"`
}

type invocation struct {
	protocol     string
	command      string
	targetKind   string
	target       string
	authSupplied bool
	agent        string
}

func classify(argv []string) (invocation, bool) {
	agent, namespace, arguments, completion, ok := rootInvocation(argv)
	if !ok || completion {
		return invocation{}, false
	}
	switch namespace {
	case ProtocolMCP:
		classified, ok := classifyMCP(arguments)
		classified.agent = agent
		return classified, ok
	case ProtocolA2A:
		classified, ok := classifyA2A(arguments)
		classified.agent = agent
		return classified, ok
	default:
		return invocation{}, false
	}
}

func rootInvocation(argv []string) (agent, namespace string, arguments []string, completion, ok bool) {
	if len(argv) < 2 {
		return "", "", nil, false, false
	}
	index := 1
	if argv[index] == "__complete" || argv[index] == "__completeNoDesc" {
		completion = true
		index++
	}
	if index < len(argv) && argv[index] == "--agent" {
		if index+1 >= len(argv) || argv[index+1] == "" {
			return "", "", nil, completion, false
		}
		agent = argv[index+1]
		index += 2
	} else if index < len(argv) && strings.HasPrefix(argv[index], "--agent=") {
		agent = strings.TrimPrefix(argv[index], "--agent=")
		if agent == "" {
			return "", "", nil, completion, false
		}
		index++
	}
	if index >= len(argv) {
		return "", "", nil, completion, false
	}
	if argv[index] == "help" && index+1 < len(argv) {
		namespace = argv[index+1]
		arguments = append([]string{"help"}, argv[index+2:]...)
		return agent, namespace, arguments, completion, namespace == ProtocolMCP || namespace == ProtocolA2A
	}
	namespace = argv[index]
	arguments = argv[index+1:]
	return agent, namespace, arguments, completion, namespace == ProtocolMCP || namespace == ProtocolA2A
}

var mcpCommands = map[string]bool{
	"status": true, "connect": true, "close": true, "restart": true,
	"login": true, "logout": true, "clean": true, "grep": true,
	"help": true, "tools-list": true, "tools-get": true, "tools-call": true,
	"tasks-list": true, "tasks-get": true, "tasks-result": true,
	"tasks-cancel": true, "prompts-list": true, "prompts-get": true,
	"resources-list": true, "resources-read": true,
	"resources-subscribe": true, "resources-unsubscribe": true,
	"resources-templates-list": true, "skills-list": true, "skills-get": true,
	"logging-set-level": true, "ping": true, "server-discover": true,
	"logs": true, "version": true,
}

var mcpAuthFlags = map[string]bool{
	"--profile": true, "--headers-file": true, "--client-id": true,
	"--client-secret": true, "--client-secret-file": true,
	"--client-key": true, "--idp-client-secret": true,
	"--idp-client-secret-file": true, "--idp-client-id": true,
}

var mcpValueFlags = map[string]bool{
	"--profile": true, "--timeout": true, "--max-chars": true,
}

func classifyMCP(arguments []string) (invocation, bool) {
	classified := invocation{protocol: ProtocolMCP}
	for _, argument := range arguments {
		name := strings.SplitN(argument, "=", 2)[0]
		if mcpAuthFlags[name] {
			classified.authSupplied = true
		}
	}
	if len(arguments) == 0 {
		classified.command = "status"
		return classified, true
	}
	if strings.HasPrefix(arguments[0], "@") {
		classified.targetKind = "session"
		classified.target = "@session"
		classified.command = "info"
		if len(arguments) > 1 && mcpCommands[arguments[1]] {
			classified.command = arguments[1]
		}
		return classified, true
	}
	commandIndex := -1
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		name, _, inline := strings.Cut(argument, "=")
		if strings.HasPrefix(argument, "-") {
			if !inline && mcpValueFlags[name] {
				index++
			}
			continue
		}
		commandIndex = index
		if mcpCommands[argument] {
			classified.command = argument
		}
		break
	}
	if commandIndex < 0 || classified.command == "" {
		classified.command = "unknown"
		return classified, true
	}
	if classified.command == "login" {
		classified.authSupplied = true
	}
	if mcpCommandHasTarget(classified.command) && commandIndex+1 < len(arguments) {
		if target := normalizeTarget(arguments[commandIndex+1]); target != "" {
			classified.targetKind = "endpoint"
			classified.target = target
		}
	}
	return classified, true
}

func mcpCommandHasTarget(command string) bool {
	switch command {
	case "connect", "login", "logout", "tools-list", "tools-get", "tools-call", "grep":
		return true
	default:
		return false
	}
}

var a2aCommands = map[string]bool{
	"card": true, "completion": true, "config": true, "help": true,
	"send": true, "server": true, "task": true, "version": true,
}

var a2aValueFlags = map[string]bool{
	"-a": true, "--agent-card": true, "--auth": true, "--config": true,
	"-e": true, "--endpoint": true, "-o": true, "--output": true,
	"--svc-param": true, "--tenant": true, "--timeout": true,
	"--transport": true,
}

func classifyA2A(arguments []string) (invocation, bool) {
	classified := invocation{protocol: ProtocolA2A, command: "unknown"}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		name, inline, hasInline := strings.Cut(argument, "=")
		if name == "--auth" {
			classified.authSupplied = true
		}
		if name == "--svc-param" {
			value := inline
			if !hasInline && index+1 < len(arguments) {
				value = arguments[index+1]
			}
			if key, _, found := strings.Cut(value, "="); found && strings.EqualFold(strings.TrimSpace(key), "authorization") {
				classified.authSupplied = true
			}
		}
		if name == "-a" || name == "--agent-card" || name == "-e" || name == "--endpoint" {
			value := inline
			if !hasInline && index+1 < len(arguments) {
				value = arguments[index+1]
			}
			if target := normalizeTarget(value); target != "" {
				classified.target = target
				classified.targetKind = "agent_card"
				if name == "-e" || name == "--endpoint" {
					classified.targetKind = "endpoint"
				}
			}
		}
		if hasInline && a2aValueFlags[name] {
			continue
		}
		if a2aValueFlags[name] {
			index++
			continue
		}
		if strings.HasPrefix(argument, "-") {
			continue
		}
		if a2aCommands[argument] {
			classified.command = argument
			if (argument == "card" || argument == "config" || argument == "task") && index+1 < len(arguments) {
				subcommand := arguments[index+1]
				if subcommand != "" && !strings.HasPrefix(subcommand, "-") {
					classified.command += "." + safeCommandPart(subcommand)
				}
			}
			break
		}
	}
	return classified, true
}

func safeCommandPart(value string) string {
	if value == "" || len(value) > 32 {
		return "unknown"
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character == '-') {
			return "unknown"
		}
	}
	return value
}

func normalizeTarget(raw string) string {
	if raw == "" || raw != strings.TrimSpace(raw) || len(raw) > 2048 {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	} else {
		parsed.Host = hostname
	}
	if port != "" {
		if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
			return ""
		}
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func eventFor(classified invocation, attemptID, phase string, started time.Time, code *int) Event {
	event := Event{
		SchemaVersion: SchemaVersion,
		AttemptID:     attemptID,
		Phase:         phase,
		Protocol:      classified.protocol,
		Command:       classified.command,
		TargetKind:    classified.targetKind,
		Target:        classified.target,
		AuthSupplied:  classified.authSupplied,
		StartedAt:     started.UTC().Format(time.RFC3339Nano),
	}
	if code != nil {
		duration := time.Since(started).Milliseconds()
		if duration < 0 {
			duration = 0
		}
		success := *code == 0
		event.DurationMS = &duration
		event.ExitCode = code
		event.Success = &success
	}
	return event
}
