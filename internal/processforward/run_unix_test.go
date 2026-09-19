//go:build !windows

package processforward

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

func TestRunPreservesStreamsAndExitCode(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code, err := Run(
		"/bin/sh",
		[]string{"-c", "read value; printf 'out:%s' \"$value\"; printf 'err' >&2; exit 23"},
		os.Environ(),
		bytes.NewBufferString("input\n"),
		&stdout,
		&stderr,
	)
	if err != nil || code != 23 || stdout.String() != "out:input" || stderr.String() != "err" {
		t.Fatalf("Run() = code %d err %v stdout %q stderr %q", code, err, stdout.String(), stderr.String())
	}
}

func TestRunReportsSignalExitCode(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		process, findErr := os.FindProcess(os.Getpid())
		if findErr == nil {
			_ = process.Signal(os.Interrupt)
		}
	}()
	code, err := Run(
		executable,
		[]string{"-test.run=^TestProcessForwardHeldChild$"},
		append(os.Environ(), "KADO_PROCESSFORWARD_HELD_CHILD=1"),
		bytes.NewReader(nil),
		io.Discard,
		io.Discard,
	)
	if err != nil || code != 130 {
		t.Fatalf("Run() = (%d, %v), want (130, nil)", code, err)
	}
}

func TestProcessForwardHeldChild(t *testing.T) {
	if os.Getenv("KADO_PROCESSFORWARD_HELD_CHILD") != "1" {
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}
