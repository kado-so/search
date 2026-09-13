//go:build !windows

package payload

import "os"

func plain(p string) error {
	s, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if s.Mode()&os.ModeSymlink != 0 {
		return ErrInvalid
	}
	return nil
}
