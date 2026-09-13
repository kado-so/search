//go:build !windows

package executablepath

import "path/filepath"

// Resolve returns the physical executable path behind a public shim or alias.
func Resolve(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
