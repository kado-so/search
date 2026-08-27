//go:build !windows

package launcher

import "os"

func commitRename(source, destination string) error {
	return os.Rename(source, destination)
}
