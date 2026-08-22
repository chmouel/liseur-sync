package webui

// Deleting a book from a browser (ADR-0025).
//
// The rules about what may be deleted where are not here. They are in
// the API's DeleteBook, which this delegates to through the Deleter
// interface, exactly as uploads delegate through Uploader: there is one
// implementation of "a book may be deleted where a book may be
// written", and two ways of asking for it.
//
// What is here is the browser's half — the session, the CSRF token, and
// turning an answer into a sentence on the page the form came from.

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Deleter removes one book from the server: its file, its catalog row,
// and optionally the caller's own reading of it. It is an interface so
// this package keeps depending on nothing but the store, and so a nil
// value hides the control rather than showing one that cannot work.
type Deleter interface {
	DeleteBookFrom(
		ctx context.Context, bookID, userID string, forgetReading bool,
	) (title string, readingForgotten, readingKept bool, err error)
}

// handleDeleteBookFile implements POST /ui/books/{id}/destroy.
//
// Named for what it does rather than matching its sibling's route: this
// server already had a /delete on a book, and the two are genuinely
// different decisions. That one retires a row whose file a pass proved
// gone; this one deletes the file.
func (s *Server) handleDeleteBookFile(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	bookID := r.PathValue("id")
	back := relPrefix(r.URL.Path) + "library"
	book, err := s.St.CatalogBookByID(r.Context(), u.ID, bookID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	back += "?folder=" + url.QueryEscape(book.FolderID)
	if s.Deletes == nil {
		s.deleteDone(w, back, "", "this server cannot delete books")
		return
	}
	title, forgotten, kept, err := s.Deletes.DeleteBookFrom(
		r.Context(), bookID, u.ID, r.FormValue("forget_reading") == "true")
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		s.deleteDone(w, back, "", err.Error())
		return
	}
	notice := "deleted " + title
	switch {
	case forgotten:
		notice += ", and your reading of it"
	case kept:
		// Not a failure worth a red banner: another copy of the same
		// book still holds the reading, so forgetting it would take a
		// position the reader is still using.
		notice += "; your reading stays with the other copy you have"
	}
	s.deleteDone(w, back, notice, "")
}
