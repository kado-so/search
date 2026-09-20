package protocoltelemetry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBeginDoesNotBlockReporterSetup(t *testing.T) {
	release := make(chan struct{})
	returned := make(chan *Attempt, 1)
	go func() {
		returned <- begin(
			true,
			[]string{"kado", "mcp", "status"},
			func(string) (reporter, error) {
				<-release
				return nil, errors.New("unavailable")
			},
		)
	}()
	select {
	case attempt := <-returned:
		close(release)
		if attempt == nil {
			t.Fatal("begin returned nil")
		}
		attempt.finish(0, time.Second)
	case <-time.After(250 * time.Millisecond):
		close(release)
		t.Fatal("begin blocked on reporter setup")
	}
}

func TestBeginDisabledDoesNotConfigureReporter(t *testing.T) {
	called := false
	attempt := begin(
		false,
		[]string{"kado", "mcp", "status"},
		func(string) (reporter, error) {
			called = true
			return nil, errors.New("must not be called")
		},
	)
	if attempt != nil {
		t.Fatal("disabled begin returned an attempt")
	}
	if called {
		t.Fatal("disabled begin configured a reporter")
	}
	// A disabled attempt remains safe for the unconditional dispatch cleanup.
	attempt.Finish(0)
}

type recordingReporter struct {
	events chan Event
}

func (reporter recordingReporter) Report(_ context.Context, event Event) error {
	reporter.events <- event
	return nil
}

func TestBeginEnabledReportsMatchingAttempt(t *testing.T) {
	events := make(chan Event, 2)
	attempt := begin(
		true,
		[]string{"kado", "mcp", "ping", "https://mcp.example/mcp"},
		func(string) (reporter, error) {
			return recordingReporter{events: events}, nil
		},
	)
	if attempt == nil {
		t.Fatal("enabled begin returned no attempt")
	}
	started := <-events
	attempt.Finish(0)
	finished := <-events
	if started.Phase != PhaseStarted || finished.Phase != PhaseFinished ||
		started.AttemptID == "" || finished.AttemptID != started.AttemptID {
		t.Fatalf("events = (%#v, %#v)", started, finished)
	}
}

func TestBeginEnabledStillSkipsCompletion(t *testing.T) {
	called := false
	attempt := begin(
		true,
		[]string{"kado", "__complete", "mcp", "tools-"},
		func(string) (reporter, error) {
			called = true
			return nil, nil
		},
	)
	if attempt != nil || called {
		t.Fatalf("completion attempt=%#v reporter called=%v", attempt, called)
	}
}

type deadlineReporter struct {
	remaining chan time.Duration
}

func (reporter deadlineReporter) Report(ctx context.Context, _ Event) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		reporter.remaining <- -1
		return nil
	}
	reporter.remaining <- time.Until(deadline)
	return nil
}

func TestFinishUsesOneOverallDeadline(t *testing.T) {
	remaining := make(chan time.Duration, 1)
	attempt := &Attempt{
		invocation: invocation{protocol: ProtocolMCP, command: "status"},
		attemptID:  "attempt",
		startedAt:  time.Now(),
		ready:      make(chan reporter, 1),
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		attempt.ready <- deadlineReporter{remaining: remaining}
	}()
	attempt.finish(0, 300*time.Millisecond)
	got := <-remaining
	if got <= 0 || got >= 275*time.Millisecond {
		t.Fatalf("finish report deadline remaining = %v, want one shared deadline", got)
	}
}
