//go:build windows

package launcher

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/kado-so/search/internal/processtree"
)

var ensureProcessTree = processtree.EnsureKillOnClose

func runPayload(payload, launcherPath string, arguments []string, stdin io.Reader, stdout, stderr io.Writer, containProcessTree bool) (int, bool) {
	if containProcessTree {
		if err := ensureProcessTree(); err != nil {
			_, _ = fmt.Fprintln(stderr, "kado: A2A process containment is unavailable [a2a_unavailable]")
			return 1, true
		}
	}
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
