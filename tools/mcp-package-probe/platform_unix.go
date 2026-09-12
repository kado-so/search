//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func checkPlainPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("linked payload path")
	}
	return nil
}

func prepareLifetime(mode string) error { return nil }

// Prototype normal-exit and signal containment; forced launcher death still
// needs the G7 ownership/supervisor design and native qualification.
func runContained(command *exec.Cmd, mode string) error {
	if mode != "foreground" {
		return command.Run()
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	defer signal.Stop(signals)
	defer close(done)
	defer syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	go func() {
		for {
			select {
			case value := <-signals:
				if sig, ok := value.(syscall.Signal); ok {
					_ = syscall.Kill(-command.Process.Pid, sig)
				}
			case <-done:
				return
			}
		}
	}()
	return command.Wait()
}
