package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kado-so/search/internal/a2adispatch"
	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/cli"
	"github.com/kado-so/search/internal/launcher"
	"github.com/kado-so/search/internal/mcpdispatch"
	"github.com/kado-so/search/internal/protocoltelemetry"
	"github.com/kado-so/search/internal/releaseclient"
)

func main() {
	info := buildinfo.Current()
	if len(os.Args) > 1 && os.Args[1] == "__uninstall-bundle" {
		if err := releaseclient.RunCompleteUninstallHelper(os.Args[2:], info); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "__install-bundle" {
		if len(os.Args) != 6 || os.Args[2] != "--directory" || os.Args[4] != "--target" {
			fmt.Fprintln(os.Stderr, "Invalid installer request.")
			os.Exit(2)
		}
		if _, err := releaseclient.InstallLocalBundle(context.Background(), os.Args[3], os.Args[5], info); err != nil {
			fmt.Fprintln(os.Stderr, "Signed Kado bundle installation failed; existing credentials were preserved.")
			os.Exit(1)
		}
		return
	}
	dispatchArguments, telemetryEnabled := rootArguments(os.Args)
	a2aInvocation := a2adispatch.Matches(dispatchArguments)
	var launchErrors io.Writer = os.Stderr
	completion := mcpdispatch.Completion(dispatchArguments)
	if completion {
		launchErrors = io.Discard
	}
	// Preserve the original arguments across launcher handoff. The selected
	// payload consumes Kado-owned root options before delegating to a sidecar.
	if code, handled := launcher.Dispatch(info, os.Args, os.Stdin, os.Stdout, launchErrors, a2aInvocation); handled {
		if completion && code != 0 {
			io.WriteString(os.Stdout, ":1\n")
			code = 0
		}
		os.Exit(code)
	}
	attempt := protocoltelemetry.Begin(telemetryEnabled, dispatchArguments)
	if code, handled := mcpdispatch.Dispatch(info, dispatchArguments, os.Stdin, os.Stdout, os.Stderr); handled {
		attempt.Finish(code)
		os.Exit(code)
	}
	if code, handled := a2adispatch.Dispatch(info, dispatchArguments, os.Stdin, os.Stdout, os.Stderr); handled {
		attempt.Finish(code)
		os.Exit(code)
	}
	os.Exit(cli.Run(dispatchArguments[1:], os.Stdout, os.Stderr, info))
}

// rootArguments removes Kado-owned root options before command dispatch. It
// deliberately stops at the command boundary so an identically named option
// after mcp or a2a remains an opaque sidecar argument.
func rootArguments(arguments []string) ([]string, bool) {
	if len(arguments) == 0 {
		return nil, false
	}
	normalized := make([]string, 0, len(arguments))
	normalized = append(normalized, arguments[0])
	index := 1
	if index < len(arguments) &&
		(arguments[index] == "__complete" || arguments[index] == "__completeNoDesc") {
		normalized = append(normalized, arguments[index])
		index++
	}
	telemetryEnabled := false
	for index < len(arguments) {
		switch {
		case arguments[index] == "--telemetry":
			telemetryEnabled = true
			index++
		case arguments[index] == "--agent":
			normalized = append(normalized, arguments[index])
			index++
			if index < len(arguments) {
				normalized = append(normalized, arguments[index])
				index++
			}
		case strings.HasPrefix(arguments[index], "--agent="):
			normalized = append(normalized, arguments[index])
			index++
		default:
			normalized = append(normalized, arguments[index:]...)
			return normalized, telemetryEnabled
		}
	}
	return normalized, telemetryEnabled
}
