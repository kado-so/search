package maintenance

import (
	"sync"
	"testing"
	"time"
)

func TestMaintenanceStateSchedulesAndRateLimitsNotice(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	state := State{Version: stateVersion}
	if !Due(state, now) {
		t.Fatal("new maintenance state is not due")
	}
	state = Complete(state, now, "0.1.0", "0.2.0", true)
	if Due(state, now) || !NoticeDue(state, now) {
		t.Fatalf("completed state = %#v", state)
	}
	state.LastNoticeAt = now.Format(time.RFC3339)
	if NoticeDue(state, now.Add(23*time.Hour)) ||
		!NoticeDue(state, now.Add(24*time.Hour)) {
		t.Fatal("notice rate limit is invalid")
	}
}

func TestConcurrentReservationsSpawnAndNoticeOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	if err := Write(root, State{
		Version:          stateVersion,
		LatestCLIVersion: "0.2.0",
		UpdateAvailable:  true,
	}); err != nil {
		t.Fatal(err)
	}

	const workers = 32
	var wait sync.WaitGroup
	results := make(chan Reservation, workers)
	errors := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			reservation, err := Reserve(root, now)
			results <- reservation
			errors <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	spawned, noticed := 0, 0
	for result := range results {
		if result.Spawn {
			spawned++
		}
		if result.NoticeVersion != "" {
			noticed++
		}
	}
	if spawned != 1 || noticed != 1 {
		t.Fatalf("spawned=%d noticed=%d, want one each", spawned, noticed)
	}
}

func TestMaintenanceStateRoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	want := State{
		Version:             stateVersion,
		LastCheckedAt:       "2026-07-29T00:00:00Z",
		NextCheckAt:         "2026-07-29T06:00:00Z",
		InstalledCLIVersion: "0.1.0",
		LatestCLIVersion:    "0.2.0",
		UpdateAvailable:     true,
	}
	if err := Write(root, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Read() = %#v, want %#v", got, want)
	}
}
