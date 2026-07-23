package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/diagnostic"
)

func TestHelpFormsAreBoundedAndSilentOnStderr(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		var stdout, stderr bytes.Buffer
		exitCode := Run(args, &stdout, &stderr, buildinfo.Info{})

		if exitCode != 0 {
			t.Fatalf("Run(%q) exit code = %d", args, exitCode)
		}
		if stderr.Len() != 0 {
			t.Fatalf("Run(%q) stderr = %q", args, stderr.String())
		}
		if stdout.Len() == 0 || stdout.Len() > 1024 {
			t.Fatalf("Run(%q) help length = %d", args, stdout.Len())
		}
	}
}

func TestVersionFormsAreSingleLineAndBounded(t *testing.T) {
	t.Parallel()

	info := buildinfo.Info{Version: strings.Repeat("v", 100), Commit: "abc\nsecret", Date: "now"}
	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		exitCode := Run(args, &stdout, &stderr, info)

		if exitCode != 0 {
			t.Fatalf("Run(%q) exit code = %d", args, exitCode)
		}
		if stderr.Len() != 0 {
			t.Fatalf("Run(%q) stderr = %q", args, stderr.String())
		}
		if strings.Count(stdout.String(), "\n") != 1 || stdout.Len() > 181 {
			t.Fatalf("Run(%q) version output = %q", args, stdout.String())
		}
	}
}

func TestInvalidUsageIsSafeAndUsesUsageExitCode(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"unknown"}, {"help", "extra"}, {"version", "extra"}} {
		var stdout, stderr bytes.Buffer
		exitCode := Run(args, &stdout, &stderr, buildinfo.Info{})

		if exitCode != diagnostic.ExitUsage {
			t.Fatalf("Run(%q) exit code = %d", args, exitCode)
		}
		if stdout.Len() != 0 {
			t.Fatalf("Run(%q) stdout = %q", args, stdout.String())
		}
		if stderr.Len() == 0 || stderr.Len() > 512 || strings.Count(stderr.String(), "\n") != 1 {
			t.Fatalf("Run(%q) stderr = %q", args, stderr.String())
		}
	}
}
