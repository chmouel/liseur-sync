package webui

import (
	"net/http"
	"net/url"
	"strings"

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

func (s *Server) handleAdminFolders(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	s.renderAdminFolders(w, r, a, u, Flash{})
}

func (s *Server) renderAdminFolders(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	after := r.URL.Query().Get("after")
	// One row more than the page shows: the extra row is the answer to
	// "is there another page", with no second counting query.
	folders, err := s.St.ListFolders(r.Context(), after, adminFoldersPerPage+1)
	if err != nil {
		http.Error(w, "folder list unavailable", http.StatusInternalServerError)
		return
	}
	prefix := relPrefix(r.URL.Path)
	var next string
	if len(folders) > adminFoldersPerPage {
		folders = folders[:adminFoldersPerPage]
		next = prefix + "admin/folders?after=" +
			url.QueryEscape(store.FolderCursor(folders[len(folders)-1]))
	}
	views := make([]adminFolderView, 0, len(folders))
	for _, folder := range folders {
		views = append(views, adminFolderView{Folder: folder})
	}
	adminPage("Folders", prefix, uiCtx(r, u), csrfFor(a), "folders",
		adminFoldersBody(prefix, csrfFor(a), views, next,
			s.Cfg.Content.FolderRoots, flash)).
		Render(r.Context(), w)
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
