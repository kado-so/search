//go:build !windows

package launcher

import (
	"io"
	"os"
	"syscall"
)

func runPayload(payload, launcherPath string, arguments []string, _ io.Reader, _, _ io.Writer) (int, bool) {
	if len(arguments) == 0 {
		arguments = []string{payload}
	} else {
		arguments = append([]string{payload}, arguments[1:]...)
	}
	if err := syscall.Exec(payload, arguments, withLaunchEnvironment(os.Environ(), launcherPath, payload)); err != nil {
		return 0, false
	}
	return 0, true
}
