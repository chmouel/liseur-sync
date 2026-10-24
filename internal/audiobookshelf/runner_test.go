package audiobookshelf

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRunnerRetriesAndStopsOnCancellation(t *testing.T) {
	f := newABSFixture(t, []string{"Chapter 1"}, []string{"Chapter 1"})
	f.service.Cfg.Account = "absent"
	f.service.Cfg.PollInterval = 10000000
	runner := &Runner{Service: f.service, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for runner.Status().Consecutive < 2 {
		select {
		case <-deadline.C:
			t.Fatal("runner did not retry")
		case <-tick.C:
		}
	}
	status := runner.Status()
	if !status.Enabled || status.LastError == "" || status.Errors < 2 {
		t.Fatalf("failure status: %+v", status)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner blocked shutdown")
	}
}

func TestUIMutationWaitCanBeCancelled(t *testing.T) {
	f := newABSFixture(t, []string{"Chapter 1"}, []string{"Chapter 1"})
	if err := f.service.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer f.service.unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.service.Link(ctx, f.user.ID, "item", f.bookID); err != context.Canceled {
		t.Fatalf("cancelled mutation: %v", err)
	}
}
