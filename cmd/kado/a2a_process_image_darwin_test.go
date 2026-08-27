package main

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func assertA2AProcessImage(t *testing.T, pid int, expected string) {
	t.Helper()
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		t.Fatalf("inspect process image: %v", err)
	}
	if filepath.Base(strings.TrimSpace(string(output))) != filepath.Base(expected) {
		t.Fatalf("process image = %q, want %q", strings.TrimSpace(string(output)), expected)
	}
}
