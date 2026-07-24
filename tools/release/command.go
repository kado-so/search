package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
)

func commandOutput(
	directory string,
	environment []string,
	name string,
	arguments ...string,
) ([]byte, error) {
	command := exec.Command(name, arguments...)
	command.Dir = directory
	if environment == nil {
		environment = sanitizedEnvironment()
	}
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, errors.New("release build command failed")
	}
	return stdout.Bytes(), nil
}

func sanitizedEnvironment() []string {
	source := os.Environ()
	output := make([]string, 0, len(source))
	for _, entry := range source {
		if strings.HasPrefix(entry, signingKeyEnvironment+"=") {
			continue
		}
		output = append(output, entry)
	}
	return output
}
