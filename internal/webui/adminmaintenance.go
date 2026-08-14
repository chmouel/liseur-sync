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
	// RefreshTick is how often the server looks for a library that is
	// due. A refresh that is late by less than this is not late.
	RefreshTick string
	Refresh     refreshHealth
	Now         time.Time
}

// refreshHealth is what the page says about library refreshes: counts,
// not names. Which library is failing, and why, is on the Libraries
// page next to the button that retries it — this page answers "is
// anything stuck?" (ADR-0013).
type refreshHealth struct {
	// Sources counts libraries with something to refresh at all.
	Sources int
	// Scheduled counts the ones that refresh on their own interval, and
	// Queued the ones somebody has asked for by hand and that have not
	// been picked up yet.
	Scheduled int
	Queued    int
	// Overdue counts scheduled libraries whose refresh came round more
	// than a whole interval ago. One interval of slack is deliberate: a
	// library is due the instant its interval elapses, so counting that
	// as late would make a healthy server look permanently behind.
	Overdue int
	// Failing counts libraries whose last refresh recorded an error.
	Failing int
	// Oldest is the age of the least recently refreshed source, and
	// Never counts the ones that have not been refreshed at all.
	Oldest string
	Never  int
	// Truncated says the walk stopped before the end of the list, so
	// these are counts of what was looked at rather than of everything.
	Truncated bool
}

// refreshHealthPage and refreshHealthPages bound the walk. A server
// with more libraries than this has a page that says so rather than one
// that reads every row on every render.
const (
	refreshHealthPage  = 100
	refreshHealthPages = 10
)

// libraryRefreshHealth walks the library list and totals what it finds.
// It reads through the admin-scoped list rather than the scanner's
// global one: this is a page somebody is looking at, and
// ListScannableLibraries exists for background jobs.
func (s *Server) libraryRefreshHealth(r *http.Request, now time.Time) refreshHealth {
	var health refreshHealth
	var oldest time.Time
	finish := func() refreshHealth {
		if !oldest.IsZero() {
			health.Oldest = age(now.Sub(oldest))
		}
		return health
	}
	cursor := ""
	for page := 0; page < refreshHealthPages; page++ {
		libs, err := s.St.AdminListLibraries(
			r.Context(), cursor, refreshHealthPage+1)
		if err != nil {
			health.Truncated = true
			return finish()
		}
		more := len(libs) > refreshHealthPage
		if more {
			libs = libs[:refreshHealthPage]
		}
		for _, l := range libs {
			if l.RootPath == nil || *l.RootPath == "" {
				continue
			}
			health.Sources++
			if l.LastRefreshError != nil {
				health.Failing++
			}
			if l.RefreshRequestedAt != nil {
				health.Queued++
			}
			switch {
			case l.LastRefreshAt == nil:
				health.Never++
			case oldest.IsZero() || l.LastRefreshAt.Before(oldest):
				oldest = *l.LastRefreshAt
			}
			if l.Refresh != store.LibraryRefreshInterval {
				continue
			}
			health.Scheduled++
			if now.After(l.RefreshDueAt().Add(l.RefreshInterval)) {
				health.Overdue++
			}
		}
		if !more {
			return finish()
		}
		cursor = store.LibraryCursor(libs[len(libs)-1])
	}
	health.Truncated = true
	return finish()
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
		RefreshTick:    secondsOr(s.Cfg.Content.RefreshTick, "never"),
	}
	view.Refresh = s.libraryRefreshHealth(r, view.Now)
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
