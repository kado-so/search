// Package processtree contains Windows process-tree lifetime containment.
package processtree

import (
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	jobMu    sync.Mutex
	ownedJob windows.Handle
)

// EnsureKillOnClose assigns the current process to a non-breakaway Job Object
// that terminates its descendants when this process exits. The non-inheritable
// handle is intentionally retained for the rest of the process lifetime.
func EnsureKillOnClose() error {
	jobMu.Lock()
	defer jobMu.Unlock()
	if ownedJob != 0 {
		return nil
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return errors.New("create process job")
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	result, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	)
	if err != nil || result == 0 {
		_ = windows.CloseHandle(job)
		return errors.New("configure process job")
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		_ = windows.CloseHandle(job)
		return errors.New("assign process job")
	}
	ownedJob = job
	return nil
}
