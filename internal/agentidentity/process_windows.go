//go:build windows

package agentidentity

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func processAncestry() []string {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)
	type process struct {
		parent uint32
		name   string
	}
	processes := make(map[uint32]process)
	entry := windows.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil
	}
	for {
		processes[entry.ProcessID] = process{
			parent: entry.ParentProcessID,
			name:   windows.UTF16ToString(entry.ExeFile[:]),
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	ancestry := make([]string, 0, 8)
	seen := make(map[uint32]bool)
	for id := uint32(os.Getppid()); id > 0 && len(ancestry) < 16 && !seen[id]; {
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
