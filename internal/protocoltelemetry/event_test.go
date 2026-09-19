package protocoltelemetry

import (
	"strings"
	"testing"
)

func TestClassifyPrivacyBoundedProtocolInvocations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		argv         []string
		protocol     string
		command      string
		targetKind   string
		target       string
		authSupplied bool
		agent        string
	}{
		{
			name:     "mcp direct tool call",
			argv:     []string{"kado", "--agent", "codex", "mcp", "tools-call", "https://MCP.Example:443/mcp?token=secret#private", "tool", "--args", `{"secret":"never"}`, "--profile", "work"},
			protocol: ProtocolMCP, command: "tools-call", targetKind: "endpoint",
			target: "https://mcp.example/mcp", authSupplied: true, agent: "codex",
		},
		{
			name:     "mcp login",
			argv:     []string{"kado", "mcp", "login", "https://mcp.example/auth", "--client-secret", "do-not-record"},
			protocol: ProtocolMCP, command: "login", targetKind: "endpoint",
			target: "https://mcp.example/auth", authSupplied: true,
		},
		{
			name:     "mcp named session",
			argv:     []string{"kado", "mcp", "@customer-name", "tools-call", "tool", `{"private":true}`},
			protocol: ProtocolMCP, command: "tools-call", targetKind: "session", target: "@session",
		},
		{
			name:     "mcp leading global option",
			argv:     []string{"kado", "mcp", "--profile", "login", "tools-list", "https://mcp.example/mcp"},
			protocol: ProtocolMCP, command: "tools-list", targetKind: "endpoint",
			target: "https://mcp.example/mcp", authSupplied: true,
		},
		{
			name:     "a2a card send",
			argv:     []string{"kado", "a2a", "--agent-card", "https://Agent.Example:443/.well-known/card.json?key=secret", "--auth", "Bearer private", "send", "private user message"},
			protocol: ProtocolA2A, command: "send", targetKind: "agent_card",
			target: "https://agent.example/.well-known/card.json", authSupplied: true,
		},
		{
			name:     "a2a direct endpoint",
			argv:     []string{"kado", "--agent=cursor", "a2a", "--endpoint=https://agent.example:8443/a2a#fragment", "--transport", "grpc", "task", "get", "private-task-id"},
			protocol: ProtocolA2A, command: "task.get", targetKind: "endpoint",
			target: "https://agent.example:8443/a2a", agent: "cursor",
		},
		{
			name:     "a2a authorization service parameter",
			argv:     []string{"kado", "a2a", "--svc-param", "Authorization=private", "send", "message"},
			protocol: ProtocolA2A, command: "send", authSupplied: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := classify(test.argv)
			if !ok {
				t.Fatal("classify() did not recognize invocation")
			}
			if got.protocol != test.protocol || got.command != test.command ||
				got.targetKind != test.targetKind || got.target != test.target ||
				got.authSupplied != test.authSupplied || got.agent != test.agent {
				t.Fatalf("classify() = %#v", got)
			}
			rendered := got.protocol + got.command + got.targetKind + got.target + got.agent
			for _, private := range []string{"secret", "private user message", "private-task-id", "customer-name", "do-not-record"} {
				if strings.Contains(rendered, private) {
					t.Fatalf("classification exposed %q in %q", private, rendered)
				}
			}
		})
	}
}

func TestClassifySkipsCompletionAndNonProtocolCommands(t *testing.T) {
	t.Parallel()
	for _, argv := range [][]string{
		{"kado", "search", "mcp server"},
		{"kado", "__complete", "mcp", "tools-"},
		{"kado", "__completeNoDesc", "a2a", "send"},
	} {
		if classified, ok := classify(argv); ok {
			t.Fatalf("classify(%q) = %#v", argv, classified)
		}
	}
}

func TestNormalizeTargetRejectsCredentialsAndNonHTTPReferences(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"https://EXAMPLE.com:443":                      "https://example.com/",
		"http://EXAMPLE.com:80/path?q=secret#fragment": "http://example.com/path",
		"https://example.com:8443/path":                "https://example.com:8443/path",
		"https://user:secret@example.com/mcp":          "",
		"file:///tmp/private-agent-card.json":          "",
		"relative/path":                                "",
	}
	for input, expected := range tests {
		if got := normalizeTarget(input); got != expected {
			t.Errorf("normalizeTarget(%q) = %q, want %q", input, got, expected)
		}
	}
}
