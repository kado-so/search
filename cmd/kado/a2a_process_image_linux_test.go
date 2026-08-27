package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func assertA2AProcessImage(t *testing.T, pid int, expected string) {
	t.Helper()
	actual, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		t.Fatalf("read process image: %v", err)
	}
	actualInfo, actualErr := os.Stat(actual)
	expectedInfo, expectedErr := os.Stat(expected)
	if actualErr != nil || expectedErr != nil || !os.SameFile(actualInfo, expectedInfo) {
		t.Fatalf("process image = %q, want %q (actual error=%v expected error=%v)", filepath.Clean(actual), expected, actualErr, expectedErr)
	}
}
