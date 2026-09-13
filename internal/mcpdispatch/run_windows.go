package mcpdispatch

import (
	"errors"
	"io"
	"os/exec"
)

func run(executable string, args, env []string, stdin io.Reader, stdout, stderr io.Writer, completion bool) (int, error) {
	// Named sessions use the runtime's authenticated native supervisor. An A2A
	// non-breakaway Job Object here would kill them when this invocation exits.
	c := exec.Command(executable, args...)
	c.Env, c.Stdin, c.Stdout, c.Stderr = env, stdin, stdout, stderr
	if completion {
		c.Stderr = io.Discard
	}
	err := c.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return 0, err
}
