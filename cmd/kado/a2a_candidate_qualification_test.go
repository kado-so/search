package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type a2aCommandContract struct {
	SchemaVersion   string   `json:"schema_version"`
	UpstreamCommit  string   `json:"upstream_commit"`
	UpstreamVersion string   `json:"upstream_version"`
	DisplayName     string   `json:"display_name"`
	GlobalFlags     []string `json:"global_flags"`
	Commands        []struct {
		Path       string   `json:"path"`
		LocalFlags []string `json:"local_flags"`
	} `json:"commands"`
}

func TestA2ACandidateMatchesPinnedCommandContract(t *testing.T) {
	kado, sidecar := qualificationPair(t)
	contract := loadA2ACommandContract(t)
	if contract.SchemaVersion != "kado.a2a-command-contract.v1" ||
		contract.DisplayName != "kado a2a" {
		t.Fatalf("unexpected command contract identity: %#v", contract)
	}

	environment := os.Environ()
	root := compareCandidateInvocation(t, kado, sidecar, environment, []string{"--help"})
	assertHelpContract(t, root.stdout, "", contract.GlobalFlags)
	for _, command := range contract.Commands {
		arguments := append(strings.Fields(command.Path), "--help")
		result := compareCandidateInvocation(t, kado, sidecar, environment, arguments)
		assertHelpContract(t, result.stdout, command.Path, command.LocalFlags)
	}
	compareCandidateInvocation(t, kado, sidecar, environment, []string{"--output", "json", "version"})
	invalid := compareCandidateInvocation(t, kado, sidecar, environment, []string{"not-a-reviewed-command"})
	if invalid.exitCode == 0 || invalid.stdout != "" || invalid.stderr == "" {
		t.Fatalf("unknown command did not fail identically: %+v", invalid)
	}
}

func qualificationPair(t *testing.T) (string, string) {
	t.Helper()
	binary := os.Getenv(a2aQualificationBinaryEnvironment)
	if binary == "" {
		t.Skip("set KADO_A2A_QUALIFICATION_BINARY to a built paired Kado candidate")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(filepath.Dir(binary), a2aTestBinaryName())
	for _, path := range []string{binary, sidecar} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("qualification pair member %q is unavailable: %v", path, err)
		}
	}
	return binary, sidecar
}

func loadA2ACommandContract(t *testing.T) a2aCommandContract {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join(cliModuleRoot(t), "third_party", "a2a-cli", "command-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var contract a2aCommandContract
	if err := decoder.Decode(&contract); err != nil {
		t.Fatal(err)
	}
	if contract.UpstreamCommit == "" || contract.UpstreamVersion == "" || len(contract.Commands) == 0 {
		t.Fatal("command contract is incomplete")
	}
	if !sort.StringsAreSorted(contract.GlobalFlags) {
		t.Fatal("global flags are not sorted")
	}
	lastPath := ""
	for _, command := range contract.Commands {
		if command.Path <= lastPath || !sort.StringsAreSorted(command.LocalFlags) {
			t.Fatal("command paths and local flags must be sorted")
		}
		lastPath = command.Path
	}
	return contract
}

func compareCandidateInvocation(
	t *testing.T,
	kado string,
	sidecar string,
	environment []string,
	arguments []string,
) a2aProcessResult {
	t.Helper()
	direct := runA2ATestProcess(t, sidecar, environment, "", arguments...)
	delegatedArguments := append([]string{"a2a"}, arguments...)
	delegated := runA2ATestProcess(t, kado, environment, "", delegatedArguments...)
	if direct != delegated {
		t.Fatalf("direct=%+v delegated=%+v arguments=%q", direct, delegated, arguments)
	}
	return delegated
}

func assertHelpContract(t *testing.T, output, command string, flags []string) {
	t.Helper()
	path := "kado a2a"
	if command != "" {
		path += " " + command
	}
	if !strings.Contains(output, "\n  "+path) {
		t.Fatalf("help omitted usage path %q", path)
	}
	for _, flag := range flags {
		if !strings.Contains(output, flag) {
			t.Fatalf("help for %q omitted reviewed flag %q", path, flag)
		}
	}
}
