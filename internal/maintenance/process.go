package maintenance

import (
	"os"
	"os/exec"
)

func Spawn(executable string) error {
	command := exec.Command(executable, "skill", "update", "--background")
	command.Env = append(os.Environ(), "KADO_MAINTENANCE_CHILD=1")
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	configureDetached(command)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
