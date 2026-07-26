//go:build !windows

package localstate

import "os"

func replaceStateFile(source, destination string) error {
	return os.Rename(source, destination)
}
