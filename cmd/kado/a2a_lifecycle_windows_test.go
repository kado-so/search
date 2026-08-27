package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestDirectA2ADispatchKillsTheWindowsTree(t *testing.T) {
	root := buildA2ATestPair(t, false)
	command, record := startHeldA2A(t, root, "direct.json", 0)
	publicPID := command.Process.Pid
	if record.ParentPID != publicPID || record.ChildPID == 0 {
		t.Fatalf("unexpected direct process tree: public=%d %s", publicPID, processDescription(record))
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitA2ACommand(t, command, record)
	assertWindowsProcessesExit(t, publicPID, record.PID, record.ChildPID)
}

func TestManagedA2ADispatchKillsTheWindowsTreeWithEitherLauncher(t *testing.T) {
	for _, test := range []struct {
		name      string
		terminate func(*exec.Cmd, a2aProcessRecord) error
	}{
		{
			name: "stable launcher",
			terminate: func(command *exec.Cmd, _ a2aProcessRecord) error {
				return command.Process.Kill()
			},
		},
		{
			name: "activated runtime",
			terminate: func(_ *exec.Cmd, record a2aProcessRecord) error {
				process, err := os.FindProcess(record.ParentPID)
				if err != nil {
					return err
				}
				return process.Kill()
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := buildA2ATestPair(t, true)
			command, record := startHeldA2A(t, root, test.name+".json", 0)
			publicPID := command.Process.Pid
			if record.ParentPID == publicPID || record.ChildPID == 0 {
				t.Fatalf("unexpected managed process tree: public=%d %s", publicPID, processDescription(record))
			}
			if err := test.terminate(command, record); err != nil {
				t.Fatal(err)
			}
			waitA2ACommand(t, command, record)
			assertWindowsProcessesExit(t, publicPID, record.ParentPID, record.PID, record.ChildPID)
		})
	}
}

func TestManagedA2ADispatchHandlesWindowsCtrlBreak(t *testing.T) {
	root := buildA2ATestPair(t, true)
	command, record := startHeldA2A(
		t,
		root,
		"ctrl-break.json",
		windows.CREATE_NEW_PROCESS_GROUP,
	)
	publicPID := command.Process.Pid
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(publicPID)); err != nil {
		t.Fatal(err)
	}
	waitA2ACommand(t, command, record)
	assertWindowsProcessesExit(t, publicPID, record.ParentPID, record.PID, record.ChildPID)
}

func startHeldA2A(
	t *testing.T,
	root,
	recordName string,
	creationFlags uint32,
) (*exec.Cmd, a2aProcessRecord) {
	t.Helper()
	recordPath := filepath.Join(root, recordName)
	command := exec.Command(filepath.Join(root, kadoTestBinaryName()), "a2a", "future-command")
	command.Env = append(
		a2aFixtureEnvironment(0),
		"KADO_A2A_FIXTURE_MODE=hold",
		"KADO_A2A_FIXTURE_RECORD="+recordPath,
		"KADO_A2A_FIXTURE_CHILD=1",
	)
	if creationFlags != 0 {
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: creationFlags}
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	record := a2aProcessRecord{}
	t.Cleanup(func() { terminateWindowsProcesses(command, record) })
	record = waitA2AProcessRecord(t, recordPath)
	return command, record
}

func waitA2ACommand(t *testing.T, command *exec.Cmd, record a2aProcessRecord) {
	t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	select {
	case <-waited:
		return
	case <-time.After(5 * time.Second):
		terminateWindowsProcesses(command, record)
		t.Fatal("A2A process tree did not terminate")
	}
}

func assertWindowsProcessesExit(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		allExited := true
		for _, pid := range pids {
			active, err := windowsProcessActive(pid)
			if err != nil {
				t.Fatalf("query process %d: %v", pid, err)
			}
			if active {
				allExited = false
				break
			}
		}
		if allExited {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("processes still active after containment teardown: %v", pids)
}

func windowsProcessActive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	handle, err := windows.OpenProcess(
		windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false, err
	}
	switch result {
	case windows.WAIT_OBJECT_0:
		return false, nil
	case uint32(windows.WAIT_TIMEOUT):
		return true, nil
	default:
		return false, fmt.Errorf("unexpected wait result %d", result)
	}
}

func terminateWindowsProcesses(command *exec.Cmd, record a2aProcessRecord) {
	for _, pid := range []int{record.ChildPID, record.PID, record.ParentPID} {
		if pid <= 0 {
			continue
		}
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	}
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
	}
}
