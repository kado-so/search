//go:build !windows

package launcher

import "os/exec"

func configureMaintenanceProcess(cmd *exec.Cmd) {}
