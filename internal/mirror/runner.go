package mirror

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Runner is the background half of the mirror: it holds the peer's
// credential, finds the account the mirror belongs to, and runs a pass
// at startup and then on a timer.
//
// It is deliberately hard to kill. The peer is somebody else's server
// on somebody else's uptime, and a reading mirror is an accessory to
// this one — so nothing it can report stops liseur-sync. A peer that is
// down, a credential that has been rotated, an account name that does
// not exist yet: all of them are logged and retried on a widening
// interval, because all of them are things an operator fixes without
// restarting anything. Run returns only when its context ends.
type Runner struct {
	Store store.Store
	Cfg   config.MirrorConfig
	Log   *slog.Logger

	// MaxPerPass bounds one pass. Zero takes the package default.
	MaxPerPass int

	mu     sync.Mutex
	status Status
}

// Status is the window into a job that otherwise runs unwatched. It is
// what the admin panel and the CLI read (M5); keeping it here means the
// loop is the only thing that has to know when it last worked.
type Status struct {
	// Peer and Account name what this mirror is, so a status is
	// legible without the configuration beside it.
	Peer    string
	Account string
	// Enabled is false when no mirror is configured at all, which is
	// the normal case and not a fault.
	Enabled bool

	LastAttempt *time.Time
	LastSuccess *time.Time
	LastReport  Report
	// LastError is the most recent failure, kept until something
	// succeeds. Consecutive counts how many attempts have failed in a
	// row, which is what distinguishes a blip from an outage.
	LastError   string
	Consecutive int
	// NextAttempt is when the loop intends to try again. During a
	// backoff this is the only way to tell a stalled mirror from a
	// waiting one.
	NextAttempt *time.Time
}

// maxBackoff caps the retry interval. Long enough that a peer that has
// been down all night is not being asked every five minutes, short
// enough that fixing it is followed by a sync within the hour.
const maxBackoff = 30 * time.Minute

// Status returns a copy of what the loop last did.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Run drives the mirror until ctx ends. It always returns nil: there is
// no failure here worth taking the server down for, and the caller
// treats a returned error as fatal.
func (r *Runner) Run(ctx context.Context) error {
	if !r.Cfg.Enabled {
		r.set(func(s *Status) { s.Enabled = false })
		return nil
	}
	r.set(func(s *Status) {
		s.Enabled = true
		s.Peer, s.Account = r.Cfg.Name, r.Cfg.Account
	})

	poll := time.Duration(r.Cfg.PollInterval)
	if poll <= 0 {
		poll = 5 * time.Minute
	}
	client := NewClient(r.Cfg)
	log := r.log().With("peer", r.Cfg.Name)
	log.Info("mirror starting", "base_url", r.Cfg.BaseURL,
		"account", r.Cfg.Account, "poll_interval", poll.String())

	// A syncer needs a user id, and the configuration names an
	// account. Resolving it is retried with everything else rather
	// than done once up front, because an account that does not exist
	// at boot may exist five minutes later.
	var syncer *Syncer
	wait := time.Duration(0)
	for {
		if wait > 0 {
			next := time.Now().Add(wait).UTC()
			r.set(func(s *Status) { s.NextAttempt = &next })
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				log.Info("mirror stopping")
				return nil
			case <-t.C:
			}
		} else if err := ctx.Err(); err != nil {
			return nil
		}

		if syncer == nil {
			s, err := r.build(ctx, client)
			if err != nil {
				if ctx.Err() != nil {
					log.Info("mirror stopping")
					return nil
				}
				wait = r.failed(log, poll, err)
				continue
			}
			syncer = s
		}

		now := time.Now().UTC()
		r.set(func(s *Status) { s.LastAttempt = &now })
		rep, err := syncer.Pass(ctx)
		if ctx.Err() != nil {
			log.Info("mirror stopping")
			return nil
		}
		if err == nil {
			err = rep.FirstError
		}
		if err != nil {
			r.set(func(s *Status) { s.LastReport = rep })
			wait = r.failed(log, poll, err)
			continue
		}

		done := time.Now().UTC()
		r.set(func(s *Status) {
			s.LastSuccess, s.LastReport = &done, rep
			s.LastError, s.Consecutive = "", 0
		})
		if rep.Pushed > 0 || rep.Pulled > 0 || rep.Failed > 0 {
			log.Info("mirror pass", "considered", rep.Considered,
				"pushed", rep.Pushed, "pulled", rep.Pulled,
				"skipped", rep.Skipped, "failed", rep.Failed)
		} else {
			log.Debug("mirror pass", "considered", rep.Considered)
		}
		wait = poll
	}
}

// build resolves the account and checks the credential. Both are done
// once and then held: the account id does not change, and the peer
// checks the same two headers on every later request anyway, so the
// authorization call is a legibility measure — it puts "the password
// is wrong" in the log at startup instead of leaving it to be inferred
// from a string of failed passes.
func (r *Runner) build(ctx context.Context, client *Client) (*Syncer, error) {
	u, err := r.Store.UserByName(ctx, r.Cfg.Account)
	if err != nil {
		return nil, fmt.Errorf("mirror account %q: %w", r.Cfg.Account, err)
	}
	if u.DisabledAt != nil {
		return nil, fmt.Errorf("mirror account %q is disabled", r.Cfg.Account)
	}
	if err := client.Authorize(ctx); err != nil {
		return nil, fmt.Errorf("peer credential: %w", err)
	}
	r.log().Info("mirror authorized", "peer", r.Cfg.Name, "account", r.Cfg.Account)
	return &Syncer{
		Store:        r.Store,
		Client:       client,
		Peer:         r.Cfg.Name,
		UserID:       u.ID,
		ActiveWindow: time.Duration(r.Cfg.ActiveDays) * 24 * time.Hour,
		MaxPerPass:   r.MaxPerPass,
		Log:          r.Log,
	}, nil
}

// failed records a failure and says how long to wait. The interval
// doubles from the poll interval up to maxBackoff, so a peer that is
// down is asked less and less often, and the first attempt after it
// comes back is at most half an hour late.
func (r *Runner) failed(log *slog.Logger, poll time.Duration, err error) time.Duration {
	var n int
	r.set(func(s *Status) {
		s.Consecutive++
		s.LastError = err.Error()
		n = s.Consecutive
	})
	wait := poll
	for i := 1; i < n && wait < maxBackoff; i++ {
		wait *= 2
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	// An unauthorized peer is not a transient fault and reads very
	// differently in a log: somebody has to go and change a key.
	level := slog.LevelWarn
	if errors.Is(err, ErrUnauthorized) {
		level = slog.LevelError
	}
	log.Log(context.Background(), level, "mirror pass failed",
		"error", err, "consecutive", n, "retry_in", wait.String())
	return wait
}

func (r *Runner) set(f func(*Status)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.status)
}
