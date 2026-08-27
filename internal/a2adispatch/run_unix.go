//go:build !windows

package a2adispatch

import (
	"io"
	"os"
	"syscall"
)

func runSidecar(
	sidecar string,
	arguments []string,
	_ io.Reader,
	_, _ io.Writer,
) (int, error) {
	argv := append([]string{sidecar}, arguments...)
	if err := syscall.Exec(sidecar, argv, os.Environ()); err != nil {
		return 0, err
	}
	return 0, nil
}
