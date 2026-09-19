//go:build linux

package processforward

import (
	"os/exec"
	"syscall"
)

func configureChild(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
