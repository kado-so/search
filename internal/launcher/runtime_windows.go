//go:build windows

package launcher

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

func runPayload(payload, launcherPath string, arguments []string, stdin io.Reader, stdout, stderr io.Writer) (int, bool) {
	childArguments := []string(nil)
	if len(arguments) > 1 {
		childArguments = arguments[1:]
	}
	command := exec.Command(payload, childArguments...)
	command.Env = withLaunchEnvironment(os.Environ(), launcherPath, payload)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if err == nil {
		return 0, true
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode(), true
	}
	return 0, false
}
