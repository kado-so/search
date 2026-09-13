package main

import (
	"errors"
	"github.com/kado-so/search/internal/processtree"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
)

func checkPlainPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("linked payload path")
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("reparse point in payload path")
	}
	return nil
}

func runContained(command *exec.Cmd, mode string) error { return command.Run() }
func prepareLifetime(mode string) error {
	if mode == "foreground" {
		return processtree.EnsureKillOnClose()
	}
	return nil
}
