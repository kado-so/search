package a2adispatch

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunSidecarContainmentFailureDoesNotStartSidecar(t *testing.T) {
	forced := errors.New("forced containment failure")
	original := ensureProcessTree
	ensureProcessTree = func() error { return forced }
	t.Cleanup(func() { ensureProcessTree = original })

	marker := filepath.Join(t.TempDir(), "executed")
	t.Setenv("KADO_A2A_CONTAINMENT_HELPER", marker)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	code, err := runSidecar(
		executable,
		[]string{"-test.run=^TestA2AContainmentHelper$"},
		bytes.NewReader(nil),
		&bytes.Buffer{},
		&bytes.Buffer{},
		false,
	)
	if code != 0 || !errors.Is(err, forced) {
		t.Fatalf("runSidecar() = (%d, %v), want (0, forced error)", code, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("sidecar helper executed: %v", err)
	}
}

func TestA2AContainmentHelper(t *testing.T) {
	marker := os.Getenv("KADO_A2A_CONTAINMENT_HELPER")
	if marker == "" {
		return
	}
	if err := os.WriteFile(marker, []byte("executed\n"), 0o600); err != nil {
		os.Exit(120)
	}
	os.Exit(0)
}
