package main

import (
	"os"

	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, buildinfo.Current()))
}
