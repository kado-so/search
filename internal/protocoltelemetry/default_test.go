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
