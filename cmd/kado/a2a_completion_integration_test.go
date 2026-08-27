package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/buildinfo"
)

const a2aFixtureCompletionOutput = "future-upstream\tUpstream-only completion\n--agent-card\tAgent Card location\n:4\n"

func TestA2ACompletionDelegatesTheHiddenProtocolExactly(t *testing.T) {
	for _, managed := range []bool{false, true} {
		layout := "developer"
		if managed {
			layout = "managed"
		}
		t.Run(layout, func(t *testing.T) {
			root := buildA2ATestPair(t, managed)
			for index, test := range []struct {
				name       string
				kado       []string
				direct     []string
				completion []string
			}{
				{name: "root", kado: []string{"__complete", "a2a", ""}, direct: []string{"__complete", ""}, completion: []string{"__complete", ""}},
				{name: "subcommand", kado: []string{"__complete", "a2a", "send", ""}, direct: []string{"__complete", "send", ""}, completion: []string{"__complete", "send", ""}},
				{name: "flag", kado: []string{"__complete", "a2a", "send", "--"}, direct: []string{"__complete", "send", "--"}, completion: []string{"__complete", "send", "--"}},
				{name: "flag value", kado: []string{"__complete", "a2a", "--output", ""}, direct: []string{"__complete", "--output", ""}, completion: []string{"__complete", "--output", ""}},
				{name: "filename", kado: []string{"__complete", "a2a", "send", "--agent-card", ""}, direct: []string{"__complete", "send", "--agent-card", ""}, completion: []string{"__complete", "send", "--agent-card", ""}},
				{name: "no descriptions", kado: []string{"__completeNoDesc", "a2a", "send", "--"}, direct: []string{"__completeNoDesc", "send", "--"}, completion: []string{"__completeNoDesc", "send", "--"}},
				{name: "future command", kado: []string{"__complete", "a2a", "future-command", "--future-flag", ""}, direct: []string{"__complete", "future-command", "--future-flag", ""}, completion: []string{"__complete", "future-command", "--future-flag", ""}},
				{name: "leading Kado agent", kado: []string{"__complete", "--agent", "codex", "a2a", "send", "--"}, direct: []string{"__complete", "send", "--"}, completion: []string{"__complete", "send", "--"}},
			} {
				t.Run(test.name, func(t *testing.T) {
					directRecord := filepath.Join(root, "completion-direct-"+string(rune('a'+index))+".json")
					delegatedRecord := filepath.Join(root, "completion-kado-"+string(rune('a'+index))+".json")
					directEnvironment := append(
						a2aFixtureEnvironment(0),
						"KADO_A2A_FIXTURE_MODE=completion",
						"KADO_A2A_FIXTURE_RECORD="+directRecord,
					)
					delegatedEnvironment := append(
						a2aFixtureEnvironment(0),
						"KADO_A2A_FIXTURE_MODE=completion",
						"KADO_A2A_FIXTURE_RECORD="+delegatedRecord,
					)
					direct := runA2ATestProcess(
						t,
						filepath.Join(root, a2aTestBinaryName()),
						directEnvironment,
						"",
						test.direct...,
					)
					delegated := runA2ATestProcess(
						t,
						filepath.Join(root, kadoTestBinaryName()),
						delegatedEnvironment,
						"",
						test.kado...,
					)
					if direct.exitCode != 0 || delegated.exitCode != 0 ||
						direct.stdout != a2aFixtureCompletionOutput || delegated.stdout != direct.stdout ||
						direct.stderr == "" || delegated.stderr != "" {
						t.Fatalf("direct=%+v delegated=%+v", direct, delegated)
					}
					if got := readCompletionInvocation(t, directRecord); !reflect.DeepEqual(got, test.completion) {
						t.Fatalf("direct arguments=%q want=%q", got, test.completion)
					}
					if got := readCompletionInvocation(t, delegatedRecord); !reflect.DeepEqual(got, test.completion) {
						t.Fatalf("delegated arguments=%q want=%q", got, test.completion)
					}
				})
			}
		})
	}
}

func TestA2ACompletionUnavailableSidecarReturnsQuietErrorDirective(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(string) error
	}{
		{name: "missing", mutate: os.Remove},
		{name: "tampered", mutate: func(path string) error {
			value, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value[len(value)/2] ^= 0xff
			return os.WriteFile(path, value, 0o755)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := buildA2ATestPair(t, false)
			if err := test.mutate(filepath.Join(root, a2aTestBinaryName())); err != nil {
				t.Fatal(err)
			}
			result := runA2ATestProcess(
				t,
				filepath.Join(root, kadoTestBinaryName()),
				a2aFixtureEnvironment(0),
				"",
				"__complete",
				"a2a",
				"",
			)
			if result.exitCode != 0 || result.stdout != ":1\n" || result.stderr != "" {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestA2ACompletionPreservesOfficialFailureStatusAndSuppressesDiagnostic(t *testing.T) {
	root := buildA2ATestPair(t, false)
	record := filepath.Join(root, "completion-failure.json")
	environment := append(
		a2aFixtureEnvironment(23),
		"KADO_A2A_FIXTURE_MODE=completion",
		"KADO_A2A_FIXTURE_RECORD="+record,
	)
	result := runA2ATestProcess(
		t,
		filepath.Join(root, kadoTestBinaryName()),
		environment,
		"",
		"__complete",
		"a2a",
		"send",
		"--",
	)
	if result.exitCode != 23 || result.stdout != a2aFixtureCompletionOutput || result.stderr != "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestVersionPresentationKeepsKadoAndA2AIdentitiesDistinct(t *testing.T) {
	target := runtime.GOOS + "/" + runtime.GOARCH
	ldflags := strings.Join([]string{
		"-X github.com/kado-so/search/internal/buildinfo.Version=2.3.4",
		"-X github.com/kado-so/search/internal/buildinfo.Commit=kado-commit",
		"-X github.com/kado-so/search/internal/buildinfo.Date=2026-08-27T00:00:00Z",
		"-X github.com/kado-so/search/internal/buildinfo.Target=" + target,
		"-X github.com/kado-so/search/internal/buildinfo.A2AVersion=1.0.0",
		"-X github.com/kado-so/search/internal/buildinfo.A2AUpstreamCommit=a2a-commit",
		"-X github.com/kado-so/search/internal/buildinfo.A2ADate=2026-08-26T00:00:00Z",
		"-X github.com/kado-so/search/internal/buildinfo.A2ATarget=" + target,
		"-X github.com/kado-so/search/internal/buildinfo.A2APatchSet=sha256:patch",
	}, " ")
	root := buildA2ATestPairWithLDFlags(t, false, ldflags)
	kado := filepath.Join(root, kadoTestBinaryName())
	sidecar := filepath.Join(root, a2aTestBinaryName())
	environment := a2aFixtureEnvironment(0)

	short := runA2ATestProcess(t, kado, environment, "", "--version")
	if short.exitCode != 0 || short.stderr != "" ||
		!strings.Contains(short.stdout, "kado 2.3.4") ||
		!strings.Contains(short.stdout, "kado-commit") ||
		strings.Contains(short.stdout, "a2a-commit") {
		t.Fatalf("short version=%+v", short)
	}

	human := runA2ATestProcess(t, kado, environment, "", "version")
	if human.exitCode != 0 || human.stderr != "" ||
		!strings.Contains(human.stdout, "Kado:\n") ||
		!strings.Contains(human.stdout, "A2A CLI:\n") ||
		!strings.Contains(human.stdout, "kado-commit") ||
		!strings.Contains(human.stdout, "a2a-commit") {
		t.Fatalf("human version=%+v", human)
	}

	structured := runA2ATestProcess(t, kado, environment, "", "version", "--json")
	var report buildinfo.VersionReport
	if structured.exitCode != 0 || structured.stderr != "" ||
		json.Unmarshal([]byte(structured.stdout), &report) != nil {
		t.Fatalf("structured version=%+v", structured)
	}
	sidecarBytes, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256(sidecarBytes))
	if report.SchemaVersion != buildinfo.VersionSchema ||
		report.Kado.Version != "2.3.4" || report.Kado.Commit != "kado-commit" ||
		report.Components.A2ACLI.Version != "1.0.0" ||
		report.Components.A2ACLI.UpstreamCommit != "a2a-commit" ||
		report.Components.A2ACLI.ArtifactSHA256 != wantDigest {
		t.Fatalf("version report=%+v want sidecar digest=%s", report, wantDigest)
	}

	for _, arguments := range [][]string{{"version"}, {"--output", "json", "version"}} {
		direct := runA2ATestProcess(t, sidecar, environment, "", arguments...)
		delegated := runA2ATestProcess(t, kado, environment, "", append([]string{"a2a"}, arguments...)...)
		if direct != delegated || strings.Contains(delegated.stdout, "kado-commit") {
			t.Fatalf("arguments=%q direct=%+v delegated=%+v", arguments, direct, delegated)
		}
	}
}

func readCompletionInvocation(t *testing.T, path string) []string {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var invocation a2aFixtureInvocation
	if err := json.Unmarshal(value, &invocation); err != nil {
		t.Fatal(err)
	}
	return invocation.Arguments
}
