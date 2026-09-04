package webui

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/admin"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The folders page (ADR-0013, ADR-0017).
//
// It is the one page in the whole UI that renders a root path. A folder
// has no owner. This page administers which directories the server
// reflects; each user page separately administers who may read them. That
// is a privilege beyond administering the application, which is why the
// form is bounded by content.folder_roots when an operator has set it.
//
// The server never writes below a root. Removing a folder forgets this
// server's record of it and touches nothing on disk.

// adminFoldersPerPage is how many folders one page shows.
const adminFoldersPerPage = 50

// adminFolderView is one folder as the page shows it.
type adminFolderView struct {
	Folder store.Folder
	// Granted is whether any account at all can read this folder. A
	// folder nobody is granted is catalogued, scanned and invisible, and
	// nothing else on this page would say so (ADR-0029). It is a
	// boolean, not a count: the page warns, it does not enumerate who
	// reads what.
	Granted bool
}

// Kind says in words what the disk said about this folder.
func (v adminFolderView) Kind() string {
	if v.Folder.Kind == store.FolderCalibre {
		return "Calibre library"
	}
	return "Folder of books"
}

func (s *Server) renderAdminFolders(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	if settingsRedirect(w, r, settingsAdmin, settingsAdminFolders, "", flash) {
		return
	}
	s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminFolders, "", flash, false, "", false)
}

// handleAdminCreateFolder registers a directory this server will read.
//
// Every rule about what a folder may be called and what counts as a
// usable root lives in internal/admin, so a folder added from a browser
// is the same row as one added at a shell — and the errors it returns
// are already sentences, which is why they are rendered as they are.
//
// The folder is granted to the administrator who added it, in the same
// transaction (ADR-0029). Not because administering implies reading — it
// still does not, and every other account still needs an explicit grant
// — but because a form that reports success and then shows an empty
// library is reporting the wrong thing.
func (s *Server) handleAdminCreateFolder(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Only decides where a successful add lands the browser, so a
	// failure here must not block the add itself: fall back to the
	// existing behavior (stay on the Folders page) rather than
	// aborting a mutation this check has nothing to do with validating.
	hadFolders, err := s.St.HasAnyFolder(r.Context())
	if err != nil {
		slog.Error("could not determine whether this is the first folder",
			"error", err)
		hadFolders = true
	}
	name := strings.TrimSpace(r.FormValue("name"))
	root := strings.TrimSpace(r.FormValue("root"))
	folder, err := admin.NewFolder(r.Context(), s.St, name, root,
		s.Cfg.Content.FolderRoots, u.ID)
	logAdminAction(r, u, "add-folder", name, err)
	if err != nil {
		s.renderAdminFolders(w, r, a, u, Flash{
			Error:          err.Error(),
			OpenFolderForm: true,
		})
		return
	}
	// Tell the running watcher at once. Waiting for the periodic
	// safety pass would mean an administrator adds a folder, is told it
	// is being watched, and then stares at an empty shelf for half an
	// hour.
	//
	// Add reconciles inline, and this is the one pass that is genuinely
	// expensive: a folder nothing has read yet is hashed and parsed book
	// by book. So it is bounded like the scan buttons are. Giving up
	// costs nothing — the folder is registered and watched by then, so
	// the safety pass finishes the reading, which is exactly what the
	// notice below already promises.
	if s.Watching != nil {
		ctx, cancel := context.WithTimeout(r.Context(), scanBudget)
		defer cancel()
		s.Watching.Add(ctx, folder)
	}
	notice := "Watching " + folder.Name +
		". Its books appear in your library as the server reads them. " +
		"Other accounts see it once you assign it to them from Users."
	// Nothing on the Folders page is worth stopping at once the server
	// has gone from no folders to one: the point of that first folder
	// was to see books, so go see them.
	if !hadFolders {
		redirectRel(w, relPrefix(r.URL.Path)+"library?notice="+url.QueryEscape(notice),
			http.StatusSeeOther)
		return
	}
	s.renderAdminFolders(w, r, a, u, Flash{Notice: notice})
}

// handleAdminScanFolder runs a pass over one folder now and waits for
// it.
//
// The watcher already reconciles on filesystem events and on a slow
// timer, so this is not how a folder normally stays current. It is here
// because inotify sees nothing on NFS or SMB and drops events under
// pressure: in both cases the catalog is wrong and the alternative is
// waiting half an hour for the safety pass. A pass is idempotent, so
// pressing this twice is pressing it once.
//
// It waits, like the other two scan buttons, because the question a
// person presses it with is "what is in there?" and "a pass was asked
// for" is not an answer to it. It used to hand the folder id to the
// watcher and return, on the grounds that a request should not wait for
// a pass — but a repeat pass is a walk and one stat per file (see
// unchanged() in internal/content), the wait is bounded by scanBudget,
// and on a server watching one folder this button and "Scan all" were
// the same act reported two different ways.
func (s *Server) handleAdminScanFolder(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	folderID := r.PathValue("id")
	folder, err := s.St.FolderByID(r.Context(), "", folderID)
	logAdminAction(r, u, "scan-folder", folderID, err)
	if err != nil {
		s.adminFoldersDone(w, r, a, u, Flash{Error: "no such folder"})
		return
	}
	if s.Watching == nil {
		// A server started without a watcher has nothing to ask. Saying
		// so is better than a notice claiming a pass that will not run.
		s.adminFoldersDone(w, r, a, u, Flash{
			Error: "this server is running without a folder watcher",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), scanBudget)
	defer cancel()
	res, err := s.Watching.ScanFolders(ctx, []store.Folder{folder})
	if err != nil {
		slog.ErrorContext(r.Context(), "scan failed",
			"actor_id", u.ID, "folder", folder.ID, "error", err)
		s.adminFoldersDone(w, r, a, u, Flash{Error: scanProblem(ctx, err, true)})
		return
	}
	s.adminFoldersDone(w, r, a, u, Flash{
		Notice: scanNotice(res, folder.Name+" is up to date."),
	})
}

// handleAdminScanAllFolders runs a pass over every watched folder and
// waits for it, which is what makes the button worth pressing: the page
// that comes back is the one the passes produced.
//
// It is bounded by scanBudget for the same reason the library button is
// — a pass holds the watcher's reconcile lock — and it does not stop at
// the first folder that fails, because one unreadable mount is not a
// reason to leave every other folder unscanned.
func (s *Server) handleAdminScanAllFolders(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	logAdminAction(r, u, "scan-all-folders", "", nil)
	if s.Watching == nil {
		s.adminFoldersDone(w, r, a, u, Flash{
			Error: "this server is running without a folder watcher",
		})
		return
	}
	folders, err := s.allFolders(r.Context())
	if err != nil {
		s.adminFoldersDone(w, r, a, u, Flash{
			Error: "could not list folders: " + err.Error(),
		})
		return
	}
	if len(folders) == 0 {
		s.adminFoldersDone(w, r, a, u, Flash{Notice: "No watched folders to scan."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), scanBudget)
	defer cancel()
	res, err := s.Watching.ScanFolders(ctx, folders)
	if err != nil {
		slog.ErrorContext(r.Context(), "scan-all failed", "actor_id", u.ID, "error", err)
		s.adminFoldersDone(w, r, a, u, Flash{Error: scanProblem(ctx, err, true)})
		return
	}
	s.adminFoldersDone(w, r, a, u, Flash{
		Notice: scanNotice(res, "All folders are up to date."),
	})
}

// adminFoldersDone finishes a folder mutation, for htmx and for a plain
// form post.
//
// The two need different spellings of the same destination. A 303's
// Location is resolved by the browser against the request URL, which is
// what settingsRedirect writes; htmx assigns HX-Redirect to
// location.href, which is resolved against the page the reader is on.
// The admin views always live at /ui/settings, one segment under /ui/,
// so the page-relative spelling is the bare href — and being relative is
// what keeps it right behind a proxy serving this UI under a subpath.
//
// A flash carrying a one-time secret never travels in a URL, so it falls
// through to being rendered in place, exactly as settingsRedirect does.
func (s *Server) adminFoldersDone(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	if isHTMXRequest(r) && flash.Secret == "" {
		target := settingsAdminHref("", settingsAdminFolders)
		if q := flashQuery(flash); q != "" {
			target += "&" + q
		}
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	s.renderAdminFolders(w, r, a, u, flash)
}

// handleAdminDeleteFolder forgets a folder and everything catalogued
// from it. Nothing under its root is touched: those files were never
// this server's to delete.
func (s *Server) handleAdminDeleteFolder(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	folderID := r.PathValue("id")
	folder, err := s.St.FolderByID(r.Context(), "", folderID)
	if err != nil {
		logAdminAction(r, u, "remove-folder", folderID, err)
		s.renderAdminFolders(w, r, a, u, Flash{Error: "no such folder"})
		return
	}
	err = s.St.DeleteFolder(r.Context(), folderID)
	logAdminAction(r, u, "remove-folder", folderID, err)
	if err != nil {
		s.renderAdminFolders(w, r, a, u, Flash{
			Error: "that folder could not be removed; try again",
		})
		return
	}
	if s.Watching != nil {
		s.Watching.Remove(folderID)
	}
	s.renderAdminFolders(w, r, a, u, Flash{
		Notice: "Stopped watching " + folder.Name +
			". Nothing under that directory was changed.",
	})
}

// handleAdminSetFolderUploads flips whether a folder accepts uploads.
//
// This is the only switch that lets this server write under a root
// (ADR-0023), so it lives beside the root path rather than anywhere a
// reader can reach, and it is off until somebody turns it on.
func (s *Server) handleAdminSetFolderUploads(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	folderID := r.PathValue("id")
	folder, err := s.St.FolderByID(r.Context(), "", folderID)
	if err != nil {
		logAdminAction(r, u, "folder-uploads", folderID, err)
		s.renderAdminFolders(w, r, a, u, Flash{Error: "no such folder"})
		return
	}
	accepts := !folder.AcceptsUploads
	err = s.St.SetFolderUploads(r.Context(), folderID, accepts, time.Now().UTC())
	logAdminAction(r, u, "folder-uploads", folderID, err)
	if err != nil {
		s.renderAdminFolders(w, r, a, u, Flash{
			Error: "that folder could not be changed; try again",
		})
		return
	}
	notice := folder.Name + " no longer accepts uploads."
	if accepts {
		notice = folder.Name + " accepts uploads. Books sent to it are " +
			"written into that directory; nothing already there is touched."
	}
	s.renderAdminFolders(w, r, a, u, Flash{Notice: notice})
}
