// Package cli owns command parsing and ordinary terminal output for kado.
package cli

import (
	"fmt"
	"io"

	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/diagnostic"
)

const helpText = `Kado Search command-line client

Usage:
  kado <command>

Commands:
  help       Show this help
  version    Show bounded build information

Options:
  -h, --help       Show this help
  -v, --version    Show bounded build information
`

// Run executes one CLI invocation and returns a process exit status.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	err := run(args, stdout, info)
	if err == nil {
		return 0
	}
	code, message, exitCode := diagnostic.Public(err)
	_, _ = fmt.Fprintf(stderr, "kado: %s [%s]\n", message, code)
	return exitCode
}

func run(args []string, stdout io.Writer, info buildinfo.Info) error {
	if len(args) == 0 {
		_, _ = io.WriteString(stdout, helpText)
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			return usageError("help does not accept arguments")
		}
		_, _ = io.WriteString(stdout, helpText)
		return nil
	case "version", "-v", "--version":
		if len(args) != 1 {
			return usageError("version does not accept arguments")
		}
		_, _ = fmt.Fprintln(stdout, info.Line())
		return nil
	default:
		return usageError("unknown command; run 'kado help' for usage")
	}
}

func usageError(message string) error {
	return diagnostic.New("invalid_usage", message, diagnostic.ExitUsage, nil)
}
