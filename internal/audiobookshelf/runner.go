package audiobookshelf

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Status struct {
	Enabled     bool
	Version     string
	LastAttempt *time.Time
	LastSuccess *time.Time
	LastError   string
	Consecutive int
	Errors      int
	LastReport  Report
}

type Runner struct {
	Service *Service
	Log     *slog.Logger
	mu      sync.Mutex
	status  Status
}

func (r *Runner) Status() Status { r.mu.Lock(); defer r.mu.Unlock(); return r.status }

func (r *Runner) Run(ctx context.Context) error {
	if r.Service == nil || !r.Service.Cfg.Enabled {
		return nil
	}
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	cfg := r.Service.Cfg
	poll := cfg.PollInterval.Duration()
	if poll <= 0 {
		poll = 2 * time.Minute
	}
	r.mu.Lock()
	r.status.Enabled = true
	r.mu.Unlock()
	if version, err := r.Service.Client.Version(ctx); err == nil {
		version = strings.ReplaceAll(version, cfg.APIKey, "[redacted]")
		if len(version) > 100 {
			version = version[:100]
		}
		r.mu.Lock()
		r.status.Version = version
		r.mu.Unlock()
		log.Info("audiobookshelf server", "version", version)
	}
	wait := time.Duration(0)
	for {
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		at := time.Now().UTC()
		r.mu.Lock()
		r.status.LastAttempt = &at
		r.mu.Unlock()
		passCtx, cancel := context.WithTimeout(ctx, max(time.Minute, 12*cfg.Timeout.Duration()))
		report, err := r.Service.Pass(passCtx)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = report.FirstError
		}
		if report.Malformed > 0 {
			log.Warn("audiobookshelf skipped malformed records", "count", report.Malformed)
		}
		r.mu.Lock()
		r.status.LastReport = report
		if err != nil {
			r.status.LastError = strings.ReplaceAll(err.Error(), cfg.APIKey, "[redacted]")
			r.status.Consecutive++
			r.status.Errors += max(1, report.Failed)
			wait = poll
			for i := 1; i < r.status.Consecutive && wait < 30*time.Minute; i++ {
				wait *= 2
			}
			wait = min(wait, 30*time.Minute)
			log.Warn("audiobookshelf pass failed", "error", r.status.LastError, "retry_in", wait.String())
		} else {
			done := time.Now().UTC()
			r.status.LastSuccess, r.status.LastError, r.status.Consecutive = &done, "", 0
			wait = poll
		}
		r.mu.Unlock()
	}
}
