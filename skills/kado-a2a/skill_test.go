package kado_a2a

import (
	"strings"
	"testing"
)

func TestA2ASkillOwnsAgentInvocationWorkflow(t *testing.T) {
	files, err := Bundle()
	if err != nil {
		t.Fatal(err)
	}
	content := string(files["SKILL.md"])
	for _, required := range []string{
		"name: kado-a2a",
		"kado a2a version",
		"kado a2a --help",
		"use.agent_card",
		"--agent-card",
		"--task-id",
		"--context-id",
		"Kado account authentication and remote A2A-agent authentication are separate",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("SKILL.md is missing %q", required)
		}
	}
	if strings.Contains(content, "kado search --json") {
		t.Fatal("A2A skill contains Search instructions")
	}
	if Version() != "0.1.0" {
		t.Fatalf("Version() = %q", Version())
	}
	metadata := string(files["agents/openai.yaml"])
	for _, required := range []string{
		`display_name: "Kado A2A"`,
		"$kado-a2a",
		"allow_implicit_invocation: true",
	} {
		if !strings.Contains(metadata, required) {
			t.Fatalf("openai.yaml is missing %q", required)
		}
	}
}
