package webui

import (
	"net/http"
	"slices"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The maintenance page (ADR-0013 phase 5).
//
// It is short, because there is not much left to be stuck. Nothing is
// queued, staged or scheduled here any more: a pass reads a folder and
// writes what it found, and the only evidence worth an administrator's
// attention is how many books the last passes could not find. A run of
// missing books in one folder is almost always a disk that is not where
// it was.
//
// Every figure is an integer. No title, no path, no id — so the page
// needs none of the isolation exceptions the rest of the server is
// careful about.
//
// There are no "run now" buttons. A pass is idempotent and periodic; a
// button would add a way to start a second concurrent pass over the same
// rows to solve a problem that waiting solves.

// folderKindOrder is the order folder kinds are shown in: the ordinary
// case first, then the one that is somebody's Calibre database.
var folderKindOrder = []string{"plain", "calibre"}

// bookStatusOrder is the two states a catalogued book can be in.
var bookStatusOrder = []string{"active", "missing"}

// maintenanceView is the whole page: counts, and nothing that could
// identify what they are counting.
type maintenanceView struct {
	Counts store.AdminCounts
	Kinds  []countRow
	Books  []countRow
}

// countRow is one labelled number.
type countRow struct {
	Label string
	Count int
}

func (s *Server) handleAdminMaintenance(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.renderAdminMaintenance(w, r, a, u, Flash{})
}

func (s *Server) renderAdminMaintenance(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	counts, err := s.St.AdminCounts(r.Context())
	if err != nil {
		http.Error(w, "counts unavailable", http.StatusInternalServerError)
		return
	}
	view := maintenanceView{
		Counts: counts,
		Kinds:  countRows(counts.FoldersByKind, folderKindOrder),
		Books:  countRows(counts.BooksByStatus, bookStatusOrder),
	}
	prefix := relPrefix(r.URL.Path)
	adminPage("Maintenance", prefix, uiCtx(r, u), csrfFor(a), "maintenance",
		adminMaintenanceBody(prefix, view, flash)).
		Render(r.Context(), w)
}

// countRows lists the known keys first, in the order given, then
// anything else the database holds — so a value added later shows up as
// a number rather than disappearing.
func countRows(counts map[string]int, order []string) []countRow {
	seen := map[string]bool{}
	out := make([]countRow, 0, len(counts))
	add := func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, countRow{Label: key, Count: counts[key]})
	}
	for _, key := range order {
		add(key)
	}
	extra := make([]string, 0, len(counts))
	for key := range counts {
		if !seen[key] {
			extra = append(extra, key)
		}
	}
	slices.Sort(extra)
	for _, key := range extra {
		add(key)
	}
	return out
}
