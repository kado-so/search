//go:build !windows

package a2adispatch

import (
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func runSidecar(
	sidecar string,
	arguments []string,
	_ io.Reader,
	_, _ io.Writer,
	suppressStderr bool,
) (int, error) {
	if suppressStderr {
		null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return 0, err
		}
		defer null.Close()
		if err := unix.Dup2(int(null.Fd()), int(os.Stderr.Fd())); err != nil {
			return 0, err
		}
	}
	argv := append([]string{sidecar}, arguments...)
	if err := syscall.Exec(sidecar, argv, os.Environ()); err != nil {
		return 0, err
	}
	return 0, nil
}
