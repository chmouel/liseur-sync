package webui

// Deleting: the two ways something leaves this server from a browser.
//
// They are deliberately different things. A reader deletes their own
// work — the reading, not a file — and may only do it when no catalog
// book backs it any more, so a disk that is merely unplugged never
// costs anybody their history. An administrator deletes a catalog row
// whose file a pass already reported missing, which is a decision that
// the file is not coming back; the folder on disk is untouched either
// way, because those files were never this server's to delete
// (ADR-0017, ADR-0024).

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// handleDeleteWork forgets one work of the caller's own.
//
// The reply is empty for htmx, which swaps the card out where it stood,
// and a redirect to the library otherwise: with scripting off the form
// is a plain POST and the page has to come back.
func (s *Server) handleDeleteWork(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Relative to the page the form is on, not to this route: the
	// browser resolves a relative Location against the URL it posted to
	// (/ui/works/{id}/delete), so "library" alone would land under it.
	back := relPrefix(r.URL.Path) + "library"
	err := s.St.DeleteWork(r.Context(), u.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrInvalidInput):
		// htmx is told no with a status rather than a redirect: a 303
		// here would be followed and a whole page swapped into the hole
		// where one card was.
		if isHTMXRequest(r) {
			http.Error(w, "this book is still in the library", http.StatusConflict)
			return
		}
		s.deleteDone(w, back, "",
			"this book is still in the library, so its reading stays with it")
		return
	case err != nil:
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	s.deleteDone(w, back, "reading history deleted", "")
}

// handleDeleteMissingBook retires a catalog row a pass already marked
// missing. Admin-only, and refused for a Calibre folder: there
// metadata.db is authoritative (ADR-0022), so a book it still lists is
// put straight back by the next pass and a button that promises
// otherwise would be a lie.
func (s *Server) handleDeleteMissingBook(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	bookID := r.PathValue("id")
	book, err := s.St.CatalogBookByID(r.Context(), bookID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	back := relPrefix(r.URL.Path) + "library?folder=" + url.QueryEscape(book.FolderID)
	folder, err := s.St.FolderByID(r.Context(), book.FolderID)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if folder.Kind == store.FolderCalibre {
		s.deleteDone(w, back, "",
			"this folder's metadata.db still lists that book, so a scan would add it back")
		return
	}
	switch err := s.St.DeleteMissingBook(r.Context(), bookID); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrInvalidInput):
		s.deleteDone(w, back, "",
			"that book's file is here, so the next scan would add it back")
		return
	case err != nil:
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	s.deleteDone(w, back, "removed from the catalog", "")
}

// deleteDone sends the browser back to the page that asked, saying what
// happened. A redirect rather than a rendered page, so that reloading
// afterwards does not repeat a delete.
func (s *Server) deleteDone(w http.ResponseWriter, back, notice, problem string) {
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
