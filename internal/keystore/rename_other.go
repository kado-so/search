//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package keystore

import (
	"errors"
	"os"
)

var errAnchoredRenameUnsupported = errors.New("anchored rename is unsupported")

func renameWithinDirectory(*os.File, string, string) error {
	return errAnchoredRenameUnsupported
}
