package kado_cli_non_search

import (
	"strings"
	"testing"
)

func TestGeneralSkillOwnsNonSearchCLIWorkflow(t *testing.T) {
	files, err := Bundle()
	if err != nil {
		t.Fatal(err)
	}
	content := string(files["SKILL.md"])
	for _, required := range []string{"name: kado-cli-non-search", "kado auth link", "kado auth status", "kado agent list", "kado-search"} {
		if !strings.Contains(content, required) {
			t.Fatalf("SKILL.md is missing %q", required)
		}
	}
	if strings.Contains(content, "kado search") {
		t.Fatal("general skill contains search instructions")
	}
	if Version() != "0.1.0" {
		t.Fatalf("Version() = %q", Version())
	}
}
