package agentidentity

import (
	"path/filepath"
	"testing"
)

func TestDetectionPrecedenceAndAmbiguity(t *testing.T) {
	environment := func(values map[string]string) environmentLookup {
		return func(key string) (string, bool) {
			value, ok := values[key]
			return value, ok
		}
	}
	tests := []struct {
		name      string
		override  string
		env       map[string]string
		processes []string
		want      Detection
		wantErr   bool
	}{
		{name: "override", override: "codex", processes: []string{"claude"}, want: Detection{"codex", "override"}},
		{name: "nearest process", env: map[string]string{"CLAUDECODE": "1"}, processes: []string{"codex", "claude"}, want: Detection{"codex", "process"}},
		{name: "environment", env: map[string]string{"CLAUDECODE": "1"}, want: Detection{"claude-code", "environment"}},
		{name: "opencode process", processes: []string{"/usr/local/bin/opencode"}, want: Detection{"opencode", "process"}},
		{name: "opencode environment", env: map[string]string{"OPENCODE": "session-marker"}, want: Detection{"opencode", "environment"}},
		{name: "antigravity cli", processes: []string{filepath.Join("tools", "antigravity.exe")}, want: Detection{"antigravity", "process"}},
		{name: "antigravity helper", processes: []string{"Antigravity Helper (Renderer)"}, want: Detection{"antigravity", "process"}},
		{name: "ambiguous environment", env: map[string]string{"CLAUDECODE": "1", "CODEX_THREAD_ID": "1"}, want: Detection{Default, "default"}},
		{name: "direct", want: Detection{Default, "default"}},
		{name: "invalid override", override: "raw/path", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := detect(test.override, environment(test.env), test.processes)
			if (err != nil) != test.wantErr {
				t.Fatalf("detect error = %v", err)
			}
			if got != test.want {
				t.Fatalf("detect = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestKnownAgentsAreSortedAndValid(t *testing.T) {
	agents := Known()
	for index, agent := range agents {
		if !Valid(agent) {
			t.Fatalf("known agent %q is invalid", agent)
		}
		if index > 0 && agents[index-1] >= agent {
			t.Fatalf("known agents are not strictly sorted: %v", agents)
		}
	}
}
