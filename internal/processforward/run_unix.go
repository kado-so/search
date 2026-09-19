//go:build !windows

// Package processforward supervises a delegated CLI while preserving its
// standard streams, exit status, and ordinary termination signals.
package processforward

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Run starts and waits for a child process. Signals sent directly to the Kado
// wrapper are forwarded so the public command retains the delegated client's
// termination behavior.
func Run(
	executable string,
	arguments, environment []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) (int, error) {
	command := exec.Command(executable, arguments...)
	command.Env = environment
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	configureChild(command)

	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(signals)
	if err := command.Start(); err != nil {
		return 0, err
	}

	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	for {
		select {
		case received := <-signals:
			_ = command.Process.Signal(received)
		case err := <-waited:
			if err == nil {
				return 0, nil
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				return 0, err
			}
			if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return 128 + int(status.Signal()), nil
			}
			return exit.ExitCode(), nil
		}
	}
}
