//go:build !windows

package agentidentity

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func processAncestry() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil
	}
	type process struct {
		parent int
		name   string
	}
	processes := make(map[int]process)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		id, idErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if idErr != nil || parentErr != nil {
			continue
		}
		processes[id] = process{parent: parent, name: strings.Join(fields[2:], " ")}
	}
	ancestry := make([]string, 0, 8)
	seen := make(map[int]bool)
	for id := os.Getppid(); id > 0 && len(ancestry) < 16 && !seen[id]; {
		seen[id] = true
		process, exists := processes[id]
		if !exists {
			break
		}
		ancestry = append(ancestry, process.name)
		id = process.parent
	}
	return ancestry
}
