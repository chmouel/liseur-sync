package webui

// Asking for a pass from the library page (the ↻ button).
//
// The admin page has had a per-folder "Scan now" since folders existed,
// and it does not wait: it drops a signal in the watcher's channel and
// says "reading it again". This one does wait, because it answers a
// different question. A reader who just copied a book in wants the
// shelf to have it when the page comes back, and a page that says
// "asked for" and then shows the same shelf is not an answer.
//
// Waiting is bounded (scanBudget) and every error is generalised before
// it reaches the page: a reconcile failure names the folder's root path,
// and root_path is an administrator's to see.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// scanBudget bounds how long a scan requested from a page may run. A
// pass holds the watcher's reconcile lock, so an unbounded one started
// by any signed-in reader would stall the watcher, the periodic pass and
// folder creation for as long as a slow mount takes to walk. Giving up
// is cheap: a pass is idempotent and the safety timer runs anyway.
const scanBudget = 2 * time.Minute

// handleLibraryScan scans the libraries visible to the caller: every
// folder on the server for an administrator, every granted folder for
// everyone else.
func (s *Server) handleLibraryScan(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// back is relative to the library page, not to this route, and it
	// arrives from a form: an open redirect through a scan button would
	// be a silly way to lose the property that every /ui link stays here.
	back := r.FormValue("back")
	if !safeUIPath(back) {
		back = "library"
	}
	if s.Watching == nil {
		s.scanDone(w, r, back, "", "this server is running without a folder watcher")
		return
	}

	var folders []store.Folder
	var err error
	if u.IsAdmin {
		folders, err = s.allFolders(r.Context())
	} else {
		folders, err = s.grantedFolders(r.Context(), u.ID)
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "scan could not list folders",
			"actor_id", u.ID, "error", err)
		s.scanDone(w, r, back, "", "the folder list could not be read; try again")
		return
	}
	if len(folders) == 0 {
		s.scanDone(w, r, back, "No watched folders to scan.", "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), scanBudget)
	defer cancel()
	res, err := s.Watching.ScanFolders(ctx, folders)
	if err != nil {
		slog.ErrorContext(r.Context(), "scan failed",
			"actor_id", u.ID, "error", err)
		s.scanDone(w, r, back, "", scanProblem(ctx, err, u.IsAdmin))
		return
	}
	s.scanDone(w, r, back, scanNotice(res, "Catalog is up to date."), "")
}

// scanProblem turns a failed pass into a sentence. An administrator gets
// the error itself, because they are the person who can act on "no such
// directory". Everybody else gets a sentence with no filesystem in it:
// a reconcile error names the folder's root path, and that path is a
// filesystem oracle a reader is never shown.
func scanProblem(ctx context.Context, err error, admin bool) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "that scan is taking too long; it will finish in the background"
	}
	if admin {
		// A joined error is newline-separated, and this ends up in one
		// line of a flash.
		return "scan failed: " + strings.ReplaceAll(err.Error(), "\n", "; ")
	}
	return "that scan could not finish; an administrator can see why"
}

// scanNotice says what a pass changed. Every counter is named, because
// "up to date" and "nothing you asked about changed" are different
// answers and a purge-only pass is not the former.
func scanNotice(res store.ReconcileResult, quiet string) string {
	if !res.Changed() {
		return "Scan complete. " + quiet
	}
	parts := make([]string, 0, 7)
	for _, c := range []struct {
		n     int
		label string
	}{
		{res.Added, "added"},
		{res.Updated, "updated"},
		{res.Replaced, "replaced"},
		{res.Missing, "missing"},
		{res.Returned, "returned"},
		{res.Purged, "purged"},
		{res.Rekeyed, "rekeyed"},
	} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.label))
		}
	}
	return "Scan complete: " + strings.Join(parts, ", ") + "."
}

// allFolders returns every folder watched by this server. The empty
// viewer id is the trusted-administration read; every caller of this is
// behind an administrator check.
func (s *Server) allFolders(ctx context.Context) ([]store.Folder, error) {
	return listAllFolders(ctx, s.St, "")
}

// grantedFolders returns every folder one reader may see, which is what
// bounds a scan they asked for.
func (s *Server) grantedFolders(ctx context.Context, userID string) ([]store.Folder, error) {
	return listAllFolders(ctx, s.St, userID)
}

func listAllFolders(ctx context.Context, st store.Store, viewerID string) ([]store.Folder, error) {
	const page = 200
	var all []store.Folder
	cursor := ""
	for {
		batch, err := st.ListFolders(ctx, viewerID, cursor, page)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < page {
			return all, nil
		}
		cursor = store.FolderCursor(batch[len(batch)-1])
	}
}

// scanDone sends the browser back to the page the button was on, with
// what happened in the query string.
//
// The two ways out need different targets for the same destination,
// because they are resolved against different URLs. A 303's Location is
// resolved against the request (/ui/library/scan), so it needs the hop
// back out of this route; htmx's HX-Redirect is assigned to
// location.href and resolved against the page the reader is on
// (/ui/library), so it needs the target exactly as the page would write
// it. Sending one to the other lands on /ui/library/library or /library.
func (s *Server) scanDone(w http.ResponseWriter, r *http.Request, back, notice, problem string) {
	target := back
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	switch {
	case problem != "":
		target += sep + "problem=" + url.QueryEscape(problem)
	case notice != "":
		target += sep + "notice=" + url.QueryEscape(notice)
	}
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	redirectRel(w, relPrefix(r.URL.Path)+target, http.StatusSeeOther)
}
