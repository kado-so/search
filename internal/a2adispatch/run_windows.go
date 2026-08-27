package a2adispatch

import (
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/kado-so/search/internal/processtree"
)

var ensureProcessTree = processtree.EnsureKillOnClose

func runSidecar(
	sidecar string,
	arguments []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	suppressStderr bool,
) (int, error) {
	if err := ensureProcessTree(); err != nil {
		return 0, err
	}
	command := exec.Command(sidecar, arguments...)
	command.Env = os.Environ()
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if suppressStderr {
		command.Stderr = io.Discard
	}
	err := command.Run()
	if err == nil {
		return 0, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode(), nil
	}
	return 0, err
}
