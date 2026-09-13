//go:build !windows

package mcpdispatch

import (
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func run(executable string, args, env []string, _ io.Reader, _, _ io.Writer, completion bool) (int, error) {
	if completion {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		if err := unix.Dup2(int(f.Fd()), int(os.Stderr.Fd())); err != nil {
			return 0, err
		}
	}
	return 0, syscall.Exec(executable, append([]string{executable}, args...), env)
}
