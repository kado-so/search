//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package keystore

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameWithinDirectory(directory *os.File, oldName, newName string) error {
	descriptor := int(directory.Fd())
	return unix.Renameat(descriptor, oldName, descriptor, newName)
}
