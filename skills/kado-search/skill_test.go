package kado_search

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillScopeAndMetadata(t *testing.T) {
	content := readText(t, "SKILL.md")
	if !strings.HasPrefix(content, "---\n") ||
		!strings.Contains(content, "\nname: kado-search\n") ||
		!strings.Contains(content, "\nlicense: \"MIT\"\n") ||
		!strings.Contains(content, "\n  author: \"Kado\"\n") ||
		!strings.Contains(content, "\n  version: \"0.3.6\"\n") ||
		!strings.Contains(content, "\n  homepage: \"https://kado.so\"\n") ||
		!strings.Contains(content, "Expert at finding external solutions, specialist capabilities, or reusable resources") ||
		!strings.Contains(content, "ALWAYS use for any non-trivial task") ||
		!strings.Contains(content, "ALWAYS trigger both when the user explicitly asks for a solution") ||
		!strings.Contains(content, "since a specialist API/SaaS may still be faster") ||
		!strings.Contains(content, "Do NOT answer from memory assuming you have the required knowledge") ||
		!strings.Contains(content, "Could a current external solution produce or materially improve this outcome?") ||
		!strings.Contains(content, "APIs and hosted services") ||
		!strings.Contains(content, "kado search --json --timeout 2m") ||
		!strings.Contains(content, "This links every locally configured agent identity through one browser") ||
		!strings.Contains(content, "kado --agent <identity> auth link") {
		t.Fatal("SKILL.md metadata or Search-only scope is invalid")
	}
	lower := strings.ToLower(content)
	for _, forbidden := range []string{
		"kado update",
		"kado uninstall",
		"install kado",
		"credential store",
		"release metadata",
		"sbom",
		"provenance",
		"api key",
		"browser cookie",
		"/api/agent/",
		"agent-cli-json",
		"kado_api_key",
		"search status",
		"search refine",
		"search answer",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("SKILL.md contains forbidden instruction %q", forbidden)
		}
	}
}

func TestVersionParsesMetadataWithLFAndCRLF(t *testing.T) {
	t.Parallel()

	content := "---\nname: kado-search\nmetadata:\n  version: \"0.3.1\"\n---\n"
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "lf", content: content},
		{name: "crlf", content: strings.ReplaceAll(content, "\n", "\r\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := versionFromSkill([]byte(test.content)); got != "0.3.1" {
				t.Fatalf("versionFromSkill() = %q, want 0.3.1", got)
			}
		})
	}
}

func TestVersionRejectsMissingOrMalformedMetadata(t *testing.T) {
	t.Parallel()

	for _, content := range []string{
		"metadata:\n  version: \"0.2.2\"\n",
		"---\nmetadata:\n  version: [\n---\n",
		"---\nname: kado-search\n---\n",
	} {
		if got := versionFromSkill([]byte(content)); got != "" {
			t.Fatalf("versionFromSkill(%q) = %q, want empty", content, got)
		}
	}
}

func TestSkillAssetsAndAgentMetadata(t *testing.T) {
	metadata := readText(t, filepath.Join("agents", "openai.yaml"))
	for _, required := range []string{
		`display_name: "Kado Search"`,
		"$kado-search",
		"allow_implicit_invocation: true",
	} {
		if !strings.Contains(metadata, required) {
			t.Fatalf("openai.yaml is missing %q", required)
		}
	}
	for _, asset := range []string{
		"assets/favicon.svg",
		"assets/icon-512.gen.png",
		"assets/icon-1024.gen.png",
	} {
		if info, err := os.Stat(asset); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("missing skill asset %q", asset)
		}
	}
}

func TestDirectPluginManifestsReferenceSkill(t *testing.T) {
	root := filepath.Join("..", "..")
	codex := readJSON(t, filepath.Join(root, ".codex-plugin", "plugin.json"))
	claude := readJSON(t, filepath.Join(root, ".claude-plugin", "plugin.json"))
	marketplace := readJSON(
		t,
		filepath.Join(root, ".agents", "plugins", "marketplace.json"),
	)
	if codex["name"] != "kado-search" || codex["skills"] != "./skills/" {
		t.Fatal("Codex plugin manifest does not reference the Search skill")
	}
	if claude["name"] != "kado-search" || claude["skills"] != "./skills/" {
		t.Fatal("Claude plugin manifest does not reference the Search skill")
	}
	plugins, ok := marketplace["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatal("agent marketplace must contain exactly one plugin")
	}
	plugin, ok := plugins[0].(map[string]any)
	if !ok || plugin["name"] != "kado-search" {
		t.Fatal("agent marketplace does not reference the Search skill")
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return strings.ReplaceAll(string(encoded), "\r\n", "\n")
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("parse %q: %v", path, err)
	}
	return value
}
