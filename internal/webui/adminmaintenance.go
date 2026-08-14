package webui

import (
	"net/http"
	"slices"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The maintenance page (ADR-0013 phase 5).
//
// It reports the periodic workers by their evidence rather than by
// telemetry they do not emit: ingest jobs by state with the age of the
// oldest, books held in review, books in trash and when the next one
// expires, blobs and bytes. Every figure is an integer or a duration —
// no title, no path, no id — so the page needs none of the isolation
// exceptions the rest of the server is careful about.
//
// There are no "run now" buttons. Each worker is idempotent and
// periodic; a button would add a way to start a second concurrent pass
// over the same rows to solve a problem that waiting solves.

// jobStateOrder is the order ingest states are shown in: the path a
// file takes, then the two ways it stops.
var jobStateOrder = []string{"pending", "running", "done", "failed"}

// maintenanceView is the whole page: rows of counts, each with an
// optional "oldest" age when that state is one a job can be stuck in.
type maintenanceView struct {
	Jobs   []jobStateRow
	Counts store.AdminCounts
	// TrashRetention and OrphanGrace are what the configuration says
	// should happen, next to what the database says has happened.
	TrashRetention string
	OrphanGrace    string
	Now            time.Time
}

type jobStateRow struct {
	State string
	Count int
	// Oldest is the age of the oldest job in this state, empty for the
	// terminal states where age says nothing.
	Oldest string
}

func (s *Server) handleAdminMaintenance(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	counts, err := s.St.AdminCounts(r.Context())
	if err != nil {
		http.Error(w, "counts unavailable", http.StatusInternalServerError)
		return
	}
	view := maintenanceView{
		Counts:         counts,
		Now:            time.Now(),
		TrashRetention: hours(s.Cfg.Content.TrashRetentionHours),
		OrphanGrace:    hours(s.Cfg.Content.OrphanGraceHours),
	}
	view.Jobs = jobRows(counts, view.Now)
	prefix := relPrefix(r.URL.Path)
	adminPage("Maintenance", prefix, uiCtx(r, u), csrfFor(a), "maintenance",
		adminMaintenanceBody(prefix, view)).
		Render(r.Context(), w)
}

// jobRows lists the known states first, in the order a job moves
// through them, then anything else the database holds — so a state
// added later shows up as a number rather than disappearing.
func jobRows(counts store.AdminCounts, now time.Time) []jobStateRow {
	seen := map[string]bool{}
	var out []jobStateRow
	add := func(state string) {
		if seen[state] {
			return
		}
		seen[state] = true
		row := jobStateRow{State: state, Count: counts.JobsByState[state]}
		if oldest, ok := counts.OldestJobByState[state]; ok {
			row.Oldest = age(now.Sub(oldest))
		}
		out = append(out, row)
	}
	for _, state := range jobStateOrder {
		add(state)
	}
	extra := make([]string, 0, len(counts.JobsByState))
	for state := range counts.JobsByState {
		if !seen[state] {
			extra = append(extra, state)
		}
	}
	slices.Sort(extra)
	for _, state := range extra {
		add(state)
	}
	return out
}

// age renders a duration the way somebody reads it when they are asking
// whether something is stuck: coarse, and never more than two units.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Hours()/24), "day")
	}
}

// until renders how long is left before a moment, or "" when there is
// nothing pending.
func until(t *time.Time, now time.Time) string {
	if t == nil {
		return ""
	}
	if !t.After(now) {
		return "due now"
	}
	return "in " + age(t.Sub(now))
}
