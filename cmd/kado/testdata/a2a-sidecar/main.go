package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

type invocation struct {
	Arguments   []string `json:"arguments"`
	Input       string   `json:"input"`
	Environment string   `json:"environment"`
}

type processRecord struct {
	PID       int `json:"pid"`
	ParentPID int `json:"parent_pid"`
	ChildPID  int `json:"child_pid,omitempty"`
}

func main() {
	switch os.Getenv("KADO_A2A_FIXTURE_MODE") {
	case "hold":
		hold()
		return
	case "child":
		forever()
		return
	case "":
	default:
		os.Exit(119)
	}
	if marker := os.Getenv("KADO_A2A_FIXTURE_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte("executed\n"), 0o600); err != nil {
			os.Exit(120)
		}
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(121)
	}
	arguments := append([]string{}, os.Args[1:]...)
	if err := json.NewEncoder(os.Stdout).Encode(invocation{
		Arguments:   arguments,
		Input:       string(input),
		Environment: os.Getenv("KADO_A2A_FIXTURE_VALUE"),
	}); err != nil {
		os.Exit(122)
	}
	_, _ = os.Stderr.WriteString("fixture-stderr\n")
	if code, err := strconv.Atoi(os.Getenv("KADO_A2A_FIXTURE_EXIT")); err == nil && code > 0 && code < 126 {
		os.Exit(code)
	}
}

func hold() {
	recordPath := os.Getenv("KADO_A2A_FIXTURE_RECORD")
	if recordPath == "" {
		os.Exit(123)
	}
	record := processRecord{PID: os.Getpid(), ParentPID: os.Getppid()}
	if os.Getenv("KADO_A2A_FIXTURE_CHILD") == "1" {
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "KADO_A2A_FIXTURE_MODE=child")
		if err := child.Start(); err != nil {
			os.Exit(124)
		}
		record.ChildPID = child.Process.Pid
		_ = child.Process.Release()
	}
	encoded, err := json.Marshal(record)
	if err != nil || os.WriteFile(recordPath, append(encoded, '\n'), 0o600) != nil {
		os.Exit(125)
	}
	forever()
}

func forever() {
	for {
		time.Sleep(time.Hour)
	}
}
