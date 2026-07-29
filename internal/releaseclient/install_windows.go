//go:build windows

package releaseclient

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func (manager Manager) installCandidate(
	candidate, target, expected string,
) (bool, error) {
	parent := filepath.Dir(target)
	if filepath.Base(target) != "kado.exe" ||
		filepath.Dir(candidate) != parent {
		return false, ErrInstall
	}
	payload, err := copyUpdateFile(candidate, parent, ".kado-update-payload-*.exe")
	if err != nil {
		return false, err
	}
	helper, err := copyUpdateFile(candidate, parent, ".kado-update-helper-*.exe")
	if err != nil {
		_ = os.Remove(payload)
		return false, err
	}
	newDigest, err := snapshotExecutable(payload)
	if err != nil {
		_ = os.Remove(payload)
		_ = os.Remove(helper)
		return false, err
	}
	command := exec.Command(
		helper,
		"__update-helper",
		"--parent", strconv.Itoa(os.Getpid()),
		"--target", target,
		"--payload", payload,
		"--expected-old", expected,
		"--expected-new", newDigest,
	)
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := command.Start(); err != nil {
		_ = os.Remove(payload)
		_ = os.Remove(helper)
		return false, err
	}
	if err := command.Process.Release(); err != nil {
		return false, err
	}
	return true, nil
}

func copyUpdateFile(source, directory, pattern string) (string, error) {
	value, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(name)
		}
	}()
	if _, err := file.Write(value); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	keep = true
	return name, nil
}

// RunWindowsUpdateHelper completes an update after the original executable
// exits.
func RunWindowsUpdateHelper(arguments []string) error {
	values := make(map[string]string)
	for len(arguments) >= 2 {
		values[arguments[0]] = arguments[1]
		arguments = arguments[2:]
	}
	if len(arguments) != 0 {
		return ErrInstall
	}
	parentID, err := strconv.ParseUint(values["--parent"], 10, 32)
	if err != nil || parentID == 0 {
		return ErrInstall
	}
	target := filepath.Clean(values["--target"])
	payload := filepath.Clean(values["--payload"])
	self, err := os.Executable()
	if err != nil {
		return ErrInstall
	}
	self, _ = filepath.Abs(self)
	if !filepath.IsAbs(target) || !filepath.IsAbs(payload) ||
		filepath.Base(target) != "kado.exe" ||
		filepath.Dir(target) != filepath.Dir(payload) ||
		filepath.Dir(target) != filepath.Dir(self) ||
		!strings.HasPrefix(filepath.Base(payload), ".kado-update-payload-") ||
		!strings.HasPrefix(filepath.Base(self), ".kado-update-helper-") {
		return ErrInstall
	}
	if digest, err := snapshotExecutable(payload); err != nil ||
		digest != values["--expected-new"] {
		return ErrInstall
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(parentID))
	if err == nil {
		waitResult, waitErr := windows.WaitForSingleObject(process, 60_000)
		_ = windows.CloseHandle(process)
		if waitErr != nil || waitResult == uint32(windows.WAIT_TIMEOUT) {
			return ErrInstall
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	verify := func() error {
		command := exec.CommandContext(ctx, target, "version", "--json")
		if err := command.Run(); err != nil {
			return errors.Join(ErrInstall, err)
		}
		return nil
	}
	if err := (Manager{}).replaceExpectedAndVerify(
		payload,
		target,
		values["--expected-old"],
		verify,
	); err != nil {
		return err
	}
	refresh := exec.Command(target, "skill", "update", "--background")
	refresh.Env = append(os.Environ(), "KADO_MAINTENANCE_CHILD=1")
	refresh.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := refresh.Start(); err == nil {
		_ = refresh.Process.Release()
	}
	_ = os.Remove(self)
	return nil
}
