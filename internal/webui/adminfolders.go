package webui

import (
	"net/http"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/admin"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The folders page (ADR-0013, ADR-0017).
//
// It is the one page in the whole UI that renders a root path. A folder
// has no owner and no access list — every signed-in account sees every
// folder's books — so what is administered here is not who may read
// what, but which directories on this machine the server reflects. That
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
	s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminFolders, "", flash, false, "", false)
}

// handleAdminCreateFolder registers a directory this server will read.
//
// Every rule about what a folder may be called and what counts as a
// usable root lives in internal/admin, so a folder added from a browser
// is the same row as one added at a shell — and the errors it returns
// are already sentences, which is why they are rendered as they are.
func (s *Server) handleAdminCreateFolder(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	root := strings.TrimSpace(r.FormValue("root"))
	folder, err := admin.NewFolder(r.Context(), s.St, name, root,
		s.Cfg.Content.FolderRoots)
	logAdminAction(r, u, "add-folder", name, err)
	if err != nil {
		s.renderAdminFolders(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	// Tell the running watcher at once. Waiting for the periodic
	// safety pass would mean an administrator adds a folder, is told it
	// is being watched, and then stares at an empty shelf for half an
	// hour.
	if s.Watching != nil {
		s.Watching.Add(r.Context(), folder)
	}
	s.renderAdminFolders(w, r, a, u, Flash{
		Notice: "Watching " + folder.Name +
			". Its books appear as the server reads them.",
	})
}

// handleAdminScanFolder asks for a pass over one folder now.
//
// The watcher already reconciles on filesystem events and on a slow
// timer, so this is not how a folder normally stays current. It is here
// because inotify sees nothing on NFS or SMB and drops events under
// pressure: in both cases the catalog is wrong and the alternative is
// waiting half an hour for the safety pass. A pass is idempotent, so
// pressing this twice is pressing it once.
func (s *Server) handleAdminScanFolder(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	folderID := r.PathValue("id")
	folder, err := s.St.FolderByID(r.Context(), folderID)
	logAdminAction(r, u, "scan-folder", folderID, err)
	if err != nil {
		s.renderAdminFolders(w, r, a, u, Flash{Error: "no such folder"})
		return
	}
	if s.Watching == nil {
		// A server started without a watcher has nothing to ask. Saying
		// so is better than a notice claiming a pass that will not run.
		s.renderAdminFolders(w, r, a, u, Flash{
			Error: "this server is running without a folder watcher",
		})
		return
	}
	s.Watching.Scan(folderID)
	s.renderAdminFolders(w, r, a, u, Flash{
		Notice: "Reading " + folder.Name +
			" again. Any change appears as the pass finishes.",
	})
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
	folder, err := s.St.FolderByID(r.Context(), folderID)
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
	folder, err := s.St.FolderByID(r.Context(), folderID)
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
