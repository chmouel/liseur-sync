package webui

// Sending a book to the server from a browser (ADR-0023).
//
// The rules about what may be written where are not here. They are in
// the API's ReceiveUpload, which this delegates to through the Uploads
// interface, exactly as downloads delegate through Downloader: there is
// one implementation of "an upload is a file written into a folder that
// asked for it", and two ways of asking for it.
//
// What is here is the browser's half — the session, the CSRF token, and
// turning an answer into a sentence on the page the form came from.

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Uploader receives one publication into a folder. It is an interface so
// this package keeps depending on nothing but the store, and so a nil
// value hides the form rather than serving one that cannot work.
//
// duplicate reports that the catalog already held these bytes, which is
// a success worth phrasing differently: nothing was added, and saying
// "uploaded" would be a lie the second time.
type Uploader interface {
	ReceiveUploadTo(
		w http.ResponseWriter, r *http.Request, folder store.Folder, userID string,
	) (bookID string, duplicate bool, err error)
}

// handleLibraryUpload implements POST /ui/library/upload.
func (s *Server) handleLibraryUpload(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	// Relative to the page the form is on, not to this route: the
	// browser resolves a relative Location against the URL it posted to
	// (/ui/library/upload), so "library" would land on /ui/library/library.
	back := relPrefix(r.URL.Path) + "library"
	folderID := r.URL.Query().Get("folder")
	if folderID != "" {
		back += "?folder=" + url.QueryEscape(folderID)
	}
	// The CSRF token arrives as a form field, and reading it consumes
	// the multipart body this handler is about to stream. So it is
	// carried in the query string instead, where the check can read it
	// without the body being touched.
	if !checkCSRFValue(r.URL.Query().Get("csrf"), a) {
		s.uploadDone(w, back, "", "that form had expired; try again")
		return
	}
	if s.Uploads == nil {
		s.uploadDone(w, back, "", "this server cannot receive uploads")
		return
	}
	folder, err := s.St.FolderByID(r.Context(), u.ID, folderID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !folder.AcceptsUploads {
		s.uploadDone(w, back, "", "that folder does not accept uploads")
		return
	}
	_, duplicate, err := s.Uploads.ReceiveUploadTo(w, r, folder, u.ID)
	if err != nil {
		s.uploadDone(w, back, "", err.Error())
		return
	}
	if duplicate {
		s.uploadDone(w, back, "that book is already on this server", "")
		return
	}
	s.uploadDone(w, back, "added to "+folder.Name, "")
}

// uploadDone sends the browser back to the page the form was on, with
// what happened in the query string. A redirect rather than a rendered
// page, so that reloading afterwards does not send the file again.
func (s *Server) uploadDone(w http.ResponseWriter, back, notice, problem string) {
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	switch {
	case problem != "":
		back += sep + "problem=" + url.QueryEscape(problem)
	case notice != "":
		back += sep + "notice=" + url.QueryEscape(notice)
	}
	redirectRel(w, back, http.StatusSeeOther)
}
