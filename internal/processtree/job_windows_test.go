package processtree

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestEnsureKillOnCloseCreatesOneNonBreakawayJob(t *testing.T) {
	if err := EnsureKillOnClose(); err != nil {
		t.Fatal(err)
	}
	first := ownedJob
	if first == 0 {
		t.Fatal("process job handle was not retained")
	}
	if err := EnsureKillOnClose(); err != nil {
		t.Fatalf("idempotent call failed: %v", err)
	}
	if ownedJob != first {
		t.Fatal("idempotent call replaced the process job")
	}

	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if err := windows.QueryInformationJobObject(
		first,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
		nil,
	); err != nil {
		t.Fatalf("query process job: %v", err)
	}
	flags := limits.BasicLimitInformation.LimitFlags
	if flags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatal("process job does not terminate members on final handle close")
	}
	if flags&(windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK|windows.JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK) != 0 {
		t.Fatalf("process job permits breakaway: flags=%#x", flags)
	}
}
