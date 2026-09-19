//go:build !windows && !linux

package processforward

import "os/exec"

func configureChild(*exec.Cmd) {}
