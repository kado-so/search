package mcpdispatch

import (
	"reflect"
	"testing"
)

func TestBoundaryPreservesOpaqueMCPArguments(t *testing.T) {
	for _, test := range []struct {
		in, want   []string
		completion bool
	}{
		{[]string{"kado", "mcp", "future", "", "--agent", "mcp-owned", "--json", "héllo world"}, []string{"future", "", "--agent", "mcp-owned", "--json", "héllo world"}, false},
		{[]string{"kado", "--agent", "caller", "mcp", "--version"}, []string{"--version"}, false},
		{[]string{"kado", "help", "mcp", "@s", "tools-call"}, []string{"@s", "tools-call", "--help"}, false},
		{[]string{"kado", "__complete", "--agent=caller", "mcp", "tools-"}, []string{"__complete", "tools-"}, true},
	} {
		got, ok := requestFor(test.in)
		if !ok || !reflect.DeepEqual(got.args, test.want) || got.completion != test.completion {
			t.Fatalf("%q: %+v, %v", test.in, got, ok)
		}
	}
	for _, args := range [][]string{{"kado", "search", "mcp"}, {"kado", "a2a", "mcp"}, {"kado", "--agent", "", "mcp"}, {"kado", "__complete", "search", "mcp"}} {
		if Matches(args) {
			t.Fatalf("claimed other namespace: %q", args)
		}
	}
}
