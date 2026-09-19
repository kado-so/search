//go:build !windows

package a2adispatch

import (
	"io"
	"os"
	"syscall"

	"github.com/kado-so/search/internal/processforward"
	"golang.org/x/sys/unix"
)

func runSidecar(
	sidecar string,
	arguments []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
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
		return 0, syscall.Exec(sidecar, append([]string{sidecar}, arguments...), os.Environ())
	}
	return processforward.Run(sidecar, arguments, os.Environ(), stdin, stdout, stderr)
}
