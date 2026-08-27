package launcher

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPayloadContainmentFailureDoesNotStartPayload(t *testing.T) {
	forced := errors.New("forced containment failure")
	original := ensureProcessTree
	ensureProcessTree = func() error { return forced }
	t.Cleanup(func() { ensureProcessTree = original })

	executable, arguments, marker := launcherContainmentHelper(t)
	var stdout, stderr bytes.Buffer
	code, handled := runPayload(
		executable,
		executable,
		arguments,
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		true,
	)
	if !handled || code != 1 || stdout.Len() != 0 ||
		stderr.String() != "kado: A2A process containment is unavailable [a2a_unavailable]\n" {
		t.Fatalf("handled=%v code=%d stdout=%q stderr=%q", handled, code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("payload helper executed: %v", err)
	}
}

func TestRunPayloadDoesNotContainOrdinaryKadoInvocation(t *testing.T) {
	called := false
	original := ensureProcessTree
	ensureProcessTree = func() error {
		called = true
		return errors.New("must not be called")
	}
	t.Cleanup(func() { ensureProcessTree = original })

	executable, arguments, marker := launcherContainmentHelper(t)
	code, handled := runPayload(
		executable,
		executable,
		arguments,
		bytes.NewReader(nil),
		&bytes.Buffer{},
		&bytes.Buffer{},
		false,
	)
	if !handled || code != 0 || called {
		t.Fatalf("handled=%v code=%d containment-called=%v", handled, code, called)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("payload helper did not execute: %v", err)
	}
}

func launcherContainmentHelper(t *testing.T) (string, []string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "executed")
	t.Setenv("KADO_LAUNCHER_CONTAINMENT_HELPER", marker)
	return executable, []string{"kado", "-test.run=^TestLauncherContainmentHelper$"}, marker
}

func TestLauncherContainmentHelper(t *testing.T) {
	marker := os.Getenv("KADO_LAUNCHER_CONTAINMENT_HELPER")
	if marker == "" {
		return
	}
	if err := os.WriteFile(marker, []byte("executed\n"), 0o600); err != nil {
		os.Exit(120)
	}
	os.Exit(0)
}
