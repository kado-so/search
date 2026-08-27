package main

import (
	"os"

	"github.com/kado-so/search/internal/a2adispatch"
	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/cli"
	"github.com/kado-so/search/internal/launcher"
)

func main() {
	info := buildinfo.Current()
	if code, handled := launcher.Dispatch(info, os.Args, os.Stdin, os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	if code, handled := a2adispatch.Dispatch(info, os.Args, os.Stdin, os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, info))
}
