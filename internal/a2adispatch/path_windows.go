package a2adispatch

import "github.com/kado-so/search/internal/executablepath"

func canonicalExecutablePath(path string) (string, error) { return executablepath.Resolve(path) }
