package shellcompletion

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestRootCompletionMatchesTheKadoCommandSurface(t *testing.T) {
	var stdout bytes.Buffer
	if err := Execute([]string{HiddenComplete, ""}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"a2a", "agent", "auth", "completion", "help", "release", "search",
		"skill", "uninstall", "update", "version",
	}
	if got := completionValues(stdout.String()); !reflect.DeepEqual(got, want) {
		t.Fatalf("root completions = %q, want %q", got, want)
	}
	if !strings.HasSuffix(stdout.String(), ":4\n") {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if strings.Contains(stdout.String(), "kado-a2a") || strings.Contains(stdout.String(), "use\t") {
		t.Fatalf("root completion exposed a private or stale namespace: %q", stdout.String())
	}
}

func TestKadoFlagsAndDescriptionModes(t *testing.T) {
	for _, test := range []struct {
		command   string
		wantTab   bool
		arguments []string
	}{
		{command: HiddenComplete, wantTab: true, arguments: []string{"search", "--"}},
		{command: HiddenCompleteNoDesc, arguments: []string{"search", "--"}},
	} {
		var stdout bytes.Buffer
		arguments := append([]string{test.command}, test.arguments...)
		if err := Execute(arguments, &stdout, io.Discard); err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"--first-page", "--json", "--jsonl", "--retry", "--timeout", "--width"} {
			if !strings.Contains(stdout.String(), flag) {
				t.Fatalf("%q missing %q", stdout.String(), flag)
			}
		}
		if strings.Contains(stdout.String(), "\t") != test.wantTab {
			t.Fatalf("description mode mismatch: %q", stdout.String())
		}
		if !strings.HasSuffix(stdout.String(), ":4\n") {
			t.Fatalf("stdout=%q", stdout.String())
		}
	}
}

func TestGeneratedScriptsRegisterOnlyKado(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		for _, noDescriptions := range []bool{false, true} {
			name := shell
			arguments := []string{"completion", shell}
			hidden := HiddenComplete
			if noDescriptions {
				name += "-no-descriptions"
				arguments = append(arguments, "--no-descriptions")
				hidden = HiddenCompleteNoDesc
			}
			t.Run(name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				if err := Execute(arguments, &stdout, &stderr); err != nil {
					t.Fatalf("Execute() error = %v stderr=%q", err, stderr.String())
				}
				script := stdout.String()
				if len(script) < 1000 || len(script) > 64<<10 || !strings.Contains(script, hidden) {
					t.Fatalf("unexpected %s script length/protocol: %d", name, len(script))
				}
				if !strings.Contains(script, "kado") || strings.Contains(script, "kado-a2a __complete") ||
					strings.Contains(script, "a2a __complete") {
					t.Fatalf("%s script registered the wrong executable", name)
				}
				if stderr.Len() != 0 {
					t.Fatalf("stderr=%q", stderr.String())
				}
			})
		}
	}
}

func TestGeneratedScriptsMatchPinnedCobraSnapshots(t *testing.T) {
	snapshots := map[string]struct {
		length int
		hash   string
	}{
		"bash":                       {length: 16033, hash: "d776a763f65dd1ca2f012d1ea83983bc3ec71fc846adcaba675c58306dae0fd1"},
		"bash-no-descriptions":       {length: 16039, hash: "810474a95b2bfca7d2993cf22184513cea76bed3f45c656a6195a1501235102d"},
		"zsh":                        {length: 7676, hash: "31b830f0e7b6e4727faf235ac2f98c162f97c798d5d9972bc5f3222c607d210b"},
		"zsh-no-descriptions":        {length: 7682, hash: "b216a01a55e2a6485d9253cd543db2420f35f546ddf76e1ec60ad2e397dda6c9"},
		"fish":                       {length: 9509, hash: "7fa91ff79c6d7ed4ea946e7d52268c3e658daae2489f1cc1c7b92bff42211d4b"},
		"fish-no-descriptions":       {length: 9515, hash: "6387a042a1b9073d36e3ef964344b10eef77e6fb155ba8a655c4fcce24f2248f"},
		"powershell":                 {length: 10764, hash: "b2bc18fade42154cd6736677ee4ec41a840ffa888943fb0ded5f61f9e3fba2e1"},
		"powershell-no-descriptions": {length: 10770, hash: "bd66189c2d5c7c8e7657dfb14a3733edfd614f73d46412018cc322f832c3dedd"},
	}
	for name, snapshot := range snapshots {
		shell, noDescriptions := strings.CutSuffix(name, "-no-descriptions")
		arguments := []string{"completion", shell}
		if noDescriptions {
			arguments = append(arguments, "--no-descriptions")
		}
		var stdout bytes.Buffer
		if err := Execute(arguments, &stdout, io.Discard); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(stdout.Bytes()))
		if stdout.Len() != snapshot.length || hash != snapshot.hash {
			t.Fatalf("%s snapshot length=%d sha256=%s", name, stdout.Len(), hash)
		}
	}
}

func TestMatchesCompletionInvocations(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		matches   bool
	}{
		{arguments: []string{"completion", "bash"}, matches: true},
		{arguments: []string{"--agent", "codex", "completion", "zsh"}, matches: true},
		{arguments: []string{"--agent=codex", "completion", "fish"}, matches: true},
		{arguments: []string{HiddenComplete, "search", ""}, matches: true},
		{arguments: []string{HiddenCompleteNoDesc, "search", ""}, matches: true},
		{arguments: []string{"search", "completion"}},
		{arguments: []string{"--agent", "completion"}},
		{arguments: []string{"--agent=", "completion", "bash"}},
	} {
		if got := Matches(test.arguments); got != test.matches {
			t.Fatalf("Matches(%q) = %v, want %v", test.arguments, got, test.matches)
		}
	}
}

func completionValues(output string) []string {
	values := []string(nil)
	for _, line := range strings.Split(output, "\n") {
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		value, _, _ := strings.Cut(line, "\t")
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
