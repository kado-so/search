package launcher

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kado-so/search/internal/payload"
)

// Check the maintenance process before every deletion, including within large
// dependency trees. Losing the lease owner must stop cleanup, not just report
// an error after an unchecked recursive removal has already finished.
func removePayloadTree(root string, alive func() error) error {
	if err := payload.PlainPath(root); err != nil {
		return err
	}
	var paths []string
	if err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := alive(); err != nil {
			return err
		}
		if err := payload.PlainPath(p); err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		return err
	}
	for i := len(paths) - 1; i >= 0; i-- {
		if err := alive(); err != nil {
			return err
		}
		if err := payload.PlainPath(paths[i]); err != nil {
			return err
		}
		if err := os.Remove(paths[i]); err != nil {
			return err
		}
	}
	return nil
}
