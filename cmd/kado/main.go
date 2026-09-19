package main

import (
	"context"
	"fmt"
	"io"
	"os"

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
	a2aInvocation := a2adispatch.Matches(os.Args)
	var launchErrors io.Writer = os.Stderr
	completion := mcpdispatch.Completion(os.Args)
	if completion {
		launchErrors = io.Discard
	}
	if code, handled := launcher.Dispatch(info, os.Args, os.Stdin, os.Stdout, launchErrors, a2aInvocation); handled {
		if completion && code != 0 {
			io.WriteString(os.Stdout, ":1\n")
			code = 0
		}
		os.Exit(code)
	}
	attempt := protocoltelemetry.Begin(os.Args)
	if code, handled := mcpdispatch.Dispatch(info, os.Args, os.Stdin, os.Stdout, os.Stderr); handled {
		attempt.Finish(code)
		os.Exit(code)
	}
	if code, handled := a2adispatch.Dispatch(info, os.Args, os.Stdin, os.Stdout, os.Stderr); handled {
		attempt.Finish(code)
		os.Exit(code)
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, info))
}
