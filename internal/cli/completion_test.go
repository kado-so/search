package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/buildinfo"
)

func TestCompletionCommandGeneratesEveryKadoScript(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		for _, extra := range [][]string{nil, {"--no-descriptions"}} {
			arguments := append([]string{"completion", shell}, extra...)
			var stdout, stderr bytes.Buffer
			code := Run(arguments, &stdout, &stderr, buildinfo.Info{})
			if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "kado") {
				t.Fatalf("Run(%q) code=%d stdout-bytes=%d stderr=%q", arguments, code, stdout.Len(), stderr.String())
			}
		}
	}
}

func TestHiddenCompletionBypassesProductDependenciesAndMaintenance(t *testing.T) {
	configDirectory := t.TempDir()
	t.Setenv("KADO_CONFIG_DIR", configDirectory)
	for _, arguments := range [][]string{
		{"__complete", "search", "--"},
		{"__completeNoDesc", "search", "--"},
	} {
		var stdout, stderr bytes.Buffer
		code := runWithDependencies(
			arguments,
			&stdout,
			&stderr,
			buildinfo.Info{
				Version:            "1.2.3",
				ReleasePublicKey:   "configured",
				ReleaseMetadataURL: "https://example.test/release.json",
			},
			dependencies{},
		)
		if code != 0 || stderr.Len() != 0 || !strings.HasSuffix(stdout.String(), ":4\n") {
			t.Fatalf("arguments=%q code=%d stdout=%q stderr=%q", arguments, code, stdout.String(), stderr.String())
		}
	}
	entries, err := os.ReadDir(configDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("completion touched product state: entries=%v error=%v", entries, err)
	}
}

func TestCompletionHelpIsKadoOwned(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"completion"}, &stdout, &stderr, buildinfo.Info{})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	for _, value := range []string{"kado completion", "bash", "zsh", "fish", "powershell"} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatalf("completion help missing %q: %q", value, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "kado-a2a") {
		t.Fatalf("completion help exposed the private sidecar: %q", stdout.String())
	}
}
