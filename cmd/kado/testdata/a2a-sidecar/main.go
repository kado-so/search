package main

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
)

type invocation struct {
	Arguments   []string `json:"arguments"`
	Input       string   `json:"input"`
	Environment string   `json:"environment"`
}

func main() {
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
