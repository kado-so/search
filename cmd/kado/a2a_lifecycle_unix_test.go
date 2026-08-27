//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestA2ADispatchReplacesTheUnixProcess(t *testing.T) {
	for _, managed := range []bool{false, true} {
		layout := "developer"
		if managed {
			layout = "managed"
		}
		t.Run(layout, func(t *testing.T) {
			root := buildA2ATestPair(t, managed)
			for _, test := range []struct {
				name      string
				terminate func(*os.Process) error
			}{
				{name: "interrupt", terminate: func(process *os.Process) error { return process.Signal(os.Interrupt) }},
				{name: "forced kill", terminate: func(process *os.Process) error { return process.Kill() }},
			} {
				t.Run(test.name, func(t *testing.T) {
					command, record := startHeldUnixA2A(t, root, test.name+".json")
					publicPID := command.Process.Pid
					if record.PID != publicPID {
						t.Fatalf("Unix dispatch kept a wrapper: public=%d %s", publicPID, processDescription(record))
					}
					expectedSidecar := filepath.Join(root, a2aTestBinaryName())
					if managed {
						expectedSidecar = filepath.Join(
							root,
							kadoTestBinaryName()+".d",
							"versions",
							"9.8.7",
							a2aTestBinaryName(),
						)
					}
					assertA2AProcessImage(t, publicPID, expectedSidecar)
					if err := test.terminate(command.Process); err != nil {
						t.Fatal(err)
					}
					waitUnixA2ACommand(t, command)
					assertUnixProcessExited(t, publicPID)
				})
			}
		})
	}
}

func TestA2AUnixStartupFailureIsBounded(t *testing.T) {
	root := buildA2ATestPair(t, false)
	sidecar := filepath.Join(root, a2aTestBinaryName())
	if err := os.Chmod(sidecar, 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "executed")
	environment := append(a2aFixtureEnvironment(0), "KADO_A2A_FIXTURE_MARKER="+marker)
	result := runA2ATestProcess(
		t,
		filepath.Join(root, kadoTestBinaryName()),
		environment,
		"",
		"a2a",
		"future-command",
	)
	if result.exitCode != 1 || result.stdout != "" ||
		result.stderr != "kado: bundled A2A CLI could not start [a2a_unavailable]\n" {
		t.Fatalf("unexpected startup failure: %+v", result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("non-executable sidecar ran: %v", err)
	}
}

func TestManagedA2ACompletionReplacesTheUnixProcess(t *testing.T) {
	root := buildA2ATestPair(t, true)
	command, record := startHeldUnixA2AWithArguments(
		t,
		root,
		"completion.json",
		[]string{"__complete", "a2a", ""},
	)
	publicPID := command.Process.Pid
	if record.PID != publicPID {
		t.Fatalf("Unix completion kept a wrapper: public=%d %s", publicPID, processDescription(record))
	}
	expectedSidecar := filepath.Join(
		root,
		kadoTestBinaryName()+".d",
		"versions",
		"9.8.7",
		a2aTestBinaryName(),
	)
	assertA2AProcessImage(t, publicPID, expectedSidecar)
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitUnixA2ACommand(t, command)
	assertUnixProcessExited(t, publicPID)
}

func startHeldUnixA2A(t *testing.T, root, recordName string) (*exec.Cmd, a2aProcessRecord) {
	t.Helper()
	return startHeldUnixA2AWithArguments(
		t,
		root,
		recordName,
		[]string{"a2a", "future-command"},
	)
}

func startHeldUnixA2AWithArguments(
	t *testing.T,
	root,
	recordName string,
	arguments []string,
) (*exec.Cmd, a2aProcessRecord) {
	t.Helper()
	recordPath := filepath.Join(root, recordName)
	command := exec.Command(filepath.Join(root, kadoTestBinaryName()), arguments...)
	command.Env = append(
		a2aFixtureEnvironment(0),
		"KADO_A2A_FIXTURE_MODE=hold",
		"KADO_A2A_FIXTURE_RECORD="+recordPath,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
	})
	return command, waitA2AProcessRecord(t, recordPath)
}

func waitUnixA2ACommand(t *testing.T, command *exec.Cmd) {
	t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	select {
	case <-waited:
		return
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("Unix A2A process did not terminate")
	}
}

func assertUnixProcessExited(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("query process %d: %v", pid, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d is still active after termination", pid)
}
