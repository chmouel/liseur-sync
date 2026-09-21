package mirror

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
)

// runnerConfig points a runner at the fixture's fake peer, with
// intervals short enough that a test does not wait on a clock.
func (f *syncFixture) runnerConfig() config.MirrorConfig {
	return config.MirrorConfig{
		Enabled:      true,
		Name:         "orbit",
		BaseURL:      f.peer.URL,
		Account:      f.user.Name,
		RemoteUser:   f.peer.user,
		RemoteKey:    f.peer.key,
		DeviceID:     "liseur-sync",
		PollInterval: config.Duration(20 * time.Millisecond),
		ActiveDays:   30,
		Timeout:      config.Duration(2 * time.Second),
	}
}

func (f *syncFixture) runner() *Runner {
	return &Runner{
		Store: f.st, Cfg: f.runnerConfig(), MaxPerPass: 100,
		Log: quietLog(),
	}
}

// quietLog keeps a retry storm out of the test output; the tests
// assert on Status, not on what was logged.
func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelError + 1,
	}))
}

// waitFor polls a condition instead of sleeping for a fixed time, so a
// slow machine does not turn a passing test into a flake.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestAMirrorBuiltTheWayMainBuildsOneActuallySyncs. Every other test
// here sets MaxPerPass, which is precisely how a missing default hid:
// the store reads a limit below one as "no works", so a runner wired
// up the way the binary wires one would have polled nothing and
// reported a clean pass forever. This test is deliberately the plain
// construction, with nothing filled in that the binary does not fill.
func TestAMirrorBuiltTheWayMainBuildsOneActuallySyncs(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.33, time.Now().Add(-time.Hour))

	r := &Runner{Store: f.st, Cfg: f.runnerConfig(), Log: quietLog()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	waitFor(t, "a book to be mirrored", func() bool { return len(f.peer.pushed(t)) == 1 })
	cancel()
	<-done
}

// TestAMirrorNobodyConfiguredDoesNothing. Off is the normal state, and
// it must cost nothing and say nothing.
func TestAMirrorNobodyConfiguredDoesNothing(t *testing.T) {
	r := &Runner{Cfg: config.MirrorConfig{Enabled: false}}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("a disabled mirror returned %v", err)
	}
	if st := r.Status(); st.Enabled {
		t.Fatalf("status: %+v", st)
	}
}

// TestTheLoopSyncsAtStartupAndThenOnTheTimer is the whole point of the
// runner: reading that was here before it started goes out without
// waiting for a poll interval, and reading that happens later follows.
func TestTheLoopSyncsAtStartupAndThenOnTheTimer(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.21, time.Now().Add(-time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := f.runner()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	waitFor(t, "the startup pass", func() bool { return len(f.peer.pushed(t)) == 1 })
	if got := f.peer.pushed(t)[0]; got.Percentage != 0.21 {
		t.Fatalf("pushed %+v", got)
	}

	f.read("op-2", 0.44, time.Now())
	waitFor(t, "the second pass", func() bool { return len(f.peer.pushed(t)) == 2 })
	if got := f.peer.pushed(t)[1]; got.Percentage != 0.44 {
		t.Fatalf("pushed %+v", got)
	}

	st := r.Status()
	if !st.Enabled || st.Peer != "orbit" || st.Account != f.user.Name {
		t.Fatalf("status: %+v", st)
	}
	if st.LastSuccess == nil || st.LastError != "" || st.Consecutive != 0 {
		t.Fatalf("status after success: %+v", st)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// TestAnAccountThatDoesNotExistIsNotFatal. A typo in the configuration,
// or a mirror configured before the account it names is created, must
// leave the rest of the server running and keep trying.
func TestAnAccountThatDoesNotExistIsNotFatal(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.21, time.Now().Add(-time.Hour))

	r := f.runner()
	r.Cfg.Account = "nobody"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	waitFor(t, "a failure to be recorded", func() bool { return r.Status().Consecutive > 0 })
	if got := r.Status().LastError; !strings.Contains(got, "nobody") {
		t.Fatalf("last error: %q", got)
	}
	waitFor(t, "a retry", func() bool { return r.Status().Consecutive > 1 })
	if got := len(f.peer.pushed(t)); got != 0 {
		t.Fatalf("%d pushes for an account that does not exist", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// TestAPeerThatIsDownIsRetriedAndThenCaughtUpWith. The peer is somebody
// else's server; being unable to reach it is a normal Tuesday, and the
// only thing that matters is that reading is not lost and the mirror
// resumes on its own.
func TestAPeerThatIsDownIsRetriedAndThenCaughtUpWith(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.21, time.Now().Add(-time.Hour))
	f.peer.failWith(503, "down for maintenance")

	r := f.runner()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	waitFor(t, "the failures to be noticed", func() bool { return r.Status().Consecutive >= 2 })
	if st := r.Status(); st.LastSuccess != nil {
		t.Fatalf("a failing mirror reported success: %+v", st)
	}

	f.peer.failWith(0, "")
	waitFor(t, "the mirror to recover", func() bool { return len(f.peer.pushed(t)) > 0 })
	waitFor(t, "the failure count to clear", func() bool {
		st := r.Status()
		return st.Consecutive == 0 && st.LastError == "" && st.LastSuccess != nil
	})

	cancel()
	<-done
}

// TestBackoffWidensAndIsCapped. Without a cap a peer that is down
// overnight would be asked again in a fortnight; without widening it
// would be asked every poll interval forever.
func TestBackoffWidensAndIsCapped(t *testing.T) {
	r := &Runner{Cfg: config.MirrorConfig{Enabled: true}, Log: quietLog()}
	poll := time.Minute
	want := []time.Duration{
		time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		16 * time.Minute, maxBackoff, maxBackoff, maxBackoff,
	}
	for i, w := range want {
		if got := r.failed(r.log(), poll, context.DeadlineExceeded); got != w {
			t.Fatalf("failure %d waited %s, want %s", i+1, got, w)
		}
	}
	if got := r.Status().Consecutive; got != len(want) {
		t.Fatalf("consecutive %d", got)
	}
}

// TestShutdownDoesNotWaitOutABackoff. A mirror that has backed off to
// half an hour must not hold a restart open for half an hour.
func TestShutdownDoesNotWaitOutABackoff(t *testing.T) {
	f := newSyncFixture(t)
	r := f.runner()
	r.Cfg.Account = "nobody"
	r.Cfg.PollInterval = config.Duration(time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	waitFor(t, "the first failure", func() bool { return r.Status().Consecutive > 0 })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited out the backoff")
	}
}

// TestTheCredentialIsCheckedOnce. The peer checks the same two headers
// on every request, so authorizing repeatedly would be noise on
// somebody else's server.
func TestTheCredentialIsCheckedOnce(t *testing.T) {
	f := newSyncFixture(t)
	f.read("op-1", 0.21, time.Now().Add(-time.Hour))

	r := f.runner()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	waitFor(t, "several passes", func() bool {
		return r.Status().Consecutive == 0 && count(f, "/syncs/progress/") >= 3
	})
	cancel()
	<-done

	if got := count(f, "/users/auth"); got != 1 {
		t.Fatalf("the credential was checked %d times", got)
	}
}

// count is how many requests the peer has seen whose path contains
// fragment. A pull happens once per work per pass, so counting those
// stands in for counting passes.
func count(f *syncFixture, fragment string) int {
	n := 0
	f.peer.mu.Lock()
	defer f.peer.mu.Unlock()
	for _, r := range f.peer.requests {
		if strings.Contains(r.URL.Path, fragment) {
			n++
		}
	}
	return n
}
