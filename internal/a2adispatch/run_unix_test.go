//go:build !windows

package a2adispatch

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestRunSidecarReturnsStartupFailure(t *testing.T) {
	code, err := runSidecar(
		filepath.Join(t.TempDir(), "missing-sidecar"),
		[]string{"future-command"},
		bytes.NewReader(nil),
		&bytes.Buffer{},
		&bytes.Buffer{},
		false,
	)
	if code != 0 || err == nil {
		t.Fatalf("runSidecar() = (%d, %v), want startup error", code, err)
	}
}
