package a2adispatch

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

func runSidecar(
	sidecar string,
	arguments []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) (int, error) {
	command := exec.Command(sidecar, arguments...)
	command.Env = os.Environ()
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
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
