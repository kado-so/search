//go:build !windows

package mcpdispatch

import (
	"io"
	"os"
	"syscall"

	"github.com/kado-so/search/internal/processforward"
	"golang.org/x/sys/unix"
)

func run(executable string, args, env []string, stdin io.Reader, stdout, stderr io.Writer, completion bool) (int, error) {
	if completion {
		file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return 0, err
		}
		defer file.Close()
		if err := unix.Dup2(int(file.Fd()), int(os.Stderr.Fd())); err != nil {
			return 0, err
		}
		return 0, syscall.Exec(executable, append([]string{executable}, args...), env)
	}
	return processforward.Run(executable, args, env, stdin, stdout, stderr)
}
