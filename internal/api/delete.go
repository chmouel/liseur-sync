package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Deleting a book, ADR-0025. The counterpart of upload.go, and bounded
// the same way: only a folder somebody marked as accepting uploads, and
// only what this server could have written.

// BookRemover deletes a catalog book's file from its folder. It is an
// interface for the same two reasons BookIngest is: the API package
// stays off the content package's platform files, and a nil value
// disables the route rather than panicking.
type BookRemover interface {
	Remove(
		ctx context.Context, folder store.Folder, book store.CatalogBook,
	) error
}

// DeleteOutcome is what happened, for a caller that has to say so.
type DeleteOutcome struct {
	// Title is the book's, kept because the row is gone by the time
	// anybody phrases a sentence about it.
	Title string
	store.DeleteBookResult
}

// DeleteBook removes one catalog book: its file, then its row, and
// optionally the caller's own reading of it.
//
// Both surfaces call this — the route below and the browser control in
// the web UI — so there is one implementation of what deleting a book
// means and the two cannot drift into different ideas of it.
//
// The file goes before the row, and the order is not arbitrary. A crash
// between the two leaves a book the next pass marks missing, which is a
// state this system already models and an administrator can retire in
// one click. The reverse leaves a file the next pass puts straight back,
// which reads to the reader as a delete that silently failed.
//
// forgetReading is the caller's own reading and only ever theirs
// (ADR-0024). Declining to forget it because a second copy still holds
// it is reported, not failed.
func (s *Server) DeleteBook(
	ctx context.Context, bookID, userID string, forgetReading bool,
) (DeleteOutcome, error) {
	if s.Removal == nil {
		return DeleteOutcome{}, deleteErr(http.StatusServiceUnavailable,
			"this server is running without a folder watcher")
	}
	book, err := s.St.CatalogBookByID(ctx, userID, bookID)
	if errors.Is(err, store.ErrNotFound) {
		return DeleteOutcome{}, deleteErr(http.StatusNotFound, "no such book")
	}
	if err != nil {
		return DeleteOutcome{}, err
	}
	folder, err := s.St.FolderByID(ctx, userID, book.FolderID)
	if errors.Is(err, store.ErrNotFound) {
		return DeleteOutcome{}, deleteErr(http.StatusNotFound, "no such book")
	}
	if err != nil {
		return DeleteOutcome{}, err
	}
	// Answered the same way an upload into the same folder would be, so
	// a reader who cannot add a book here learns they cannot remove one
	// here in the same words.
	if !folder.AcceptsUploads {
		return DeleteOutcome{}, deleteErr(http.StatusForbidden,
			"this folder does not accept uploads, so this server "+
				"will not delete from it")
	}
	if err := s.Removal.Remove(ctx, folder, book); err != nil {
		return DeleteOutcome{}, removeRefusal(err)
	}

	opts := store.DeleteBookOptions{}
	if forgetReading {
		opts.ForgetReadingFor = userID
	}
	result, err := s.St.DeleteCatalogBook(ctx, bookID, opts)
	if errors.Is(err, store.ErrNotFound) {
		// The file is gone and so is the row. Somebody else got here
		// first, which is the outcome that was asked for.
		return DeleteOutcome{Title: book.Title}, nil
	}
	if err != nil {
		return DeleteOutcome{}, err
	}
	return DeleteOutcome{Title: book.Title, DeleteBookResult: result}, nil
}

// removeRefusal turns what the filesystem said into what the caller is
// told. Everything unrecognised stays a 500 with no detail: the
// messages under it name paths on this server's disk.
func removeRefusal(err error) error {
	switch {
	case errors.Is(err, content.ErrUploadsRefused):
		return deleteErr(http.StatusForbidden,
			"this folder does not accept uploads, so this server "+
				"will not delete from it")
	case errors.Is(err, content.ErrRemoveChanged):
		return deleteErr(http.StatusConflict,
			"that file has changed since it was last scanned; "+
				"it was left alone")
	case errors.Is(err, calibre.ErrLocked):
		return deleteErr(http.StatusConflict,
			"the Calibre library is busy; close Calibre and try again")
	case errors.Is(err, content.ErrUnsafePath),
		errors.Is(err, content.ErrRootMissing):
		return deleteErr(http.StatusConflict,
			"that book's file is not where the catalog says it is; "+
				"it was left alone")
	default:
		return err
	}
}

func deleteErr(status int, msg string) *WriteError {
	return &WriteError{Status: status, Message: msg}
}

// HandleDeleteBook implements DELETE /v1/books/{id}.
//
// ?forget_reading=true asks for the caller's own work to go with the
// book. The answer is 204 either way: what happened to the reading is
// reported to the browser, which has a page to say it on, and a client
// that asked already knows what it asked for.
func (s *Server) HandleDeleteBook(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	forget := r.URL.Query().Get("forget_reading") == "true"
	if _, err := s.DeleteBook(
		r.Context(), r.PathValue("id"), tok.UserID, forget,
	); err != nil {
		writeWriteError(w, err, "the delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteBookFrom is DeleteBook in the shape the web UI needs. It lives
// here so that the web package can delegate to these rules while still
// depending on nothing but the store.
func (s *Server) DeleteBookFrom(
	ctx context.Context, bookID, userID string, forgetReading bool,
) (string, bool, bool, error) {
	out, err := s.DeleteBook(ctx, bookID, userID, forgetReading)
	return out.Title, out.ReadingForgotten, out.ReadingKept, err
}
