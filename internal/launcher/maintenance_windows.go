package launcher

import (
	"os/exec"
	"syscall"
)

func configureMaintenanceProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
