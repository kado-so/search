package buildinfo

import (
	"strings"
	"testing"
)

func TestLineIsSingleLineAndBounded(t *testing.T) {
	t.Parallel()

	info := Info{
		Version: strings.Repeat("v", 100) + "\nsecret",
		Commit:  "abc123\rsecond-line",
		Date:    "",
	}
	line := info.Line()

	if strings.ContainsAny(line, "\r\n") {
		t.Fatalf("version line contains a line break: %q", line)
	}
	if len(line) > 180 {
		t.Fatalf("version line is not bounded: %d bytes", len(line))
	}
	if !strings.HasPrefix(line, "kado ") {
		t.Fatalf("version line has unexpected prefix: %q", line)
	}
}

func TestCurrentUsesBuildVariables(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
	})

	Version, Commit, Date = "v1.2.3", "abc123", "2026-07-23T00:00:00Z"

	got := Current()
	if got.Version != Version || got.Commit != Commit || got.Date != Date {
		t.Fatalf("Current() = %#v", got)
	}
}
