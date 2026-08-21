package webui

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/workident"
)

// handleBookReadingStatus records the reader's explicit read/unread choice
// as a normal position operation, so it is private to the reader and syncs
// to their other devices like any progress update.
func (s *Server) handleBookReadingStatus(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	bookID := r.PathValue("id")
	book, err := s.St.CatalogBookByID(r.Context(), bookID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.readingStatusDone(w, r, bookID, "", "could not load the book")
		return
	}

	progression, notice := 1.0, "book marked as read"
	switch r.FormValue("status") {
	case "read":
	case "unread":
		progression, notice = 0, "book marked as unread"
	default:
		s.readingStatusDone(w, r, bookID, "", "invalid reading status")
		return
	}

	workID, err := s.markWorkForBook(r, u.ID, book)
	if err != nil {
		problem := "could not find the book's reading history"
		if errors.Is(err, store.ErrConflict) {
			problem = "the book has conflicting reading histories"
		}
		s.readingStatusDone(w, r, bookID, "", problem)
		return
	}
	opID, err := s.generateSecret()
	if err != nil {
		s.readingStatusDone(w, r, bookID, "", "could not save reading status")
		return
	}
	var editionSHA *string
	if book.ContentSHA256 != "" {
		sha := book.ContentSHA256
		editionSHA = &sha
	}
	results, err := s.St.AppendOps(r.Context(), u.ID, "web-ui", []store.Op{{
		UserID: u.ID, OpID: opID, WorkID: workID, EditionSHA: editionSHA,
		ClientTS: time.Now().UTC(), Progression: progression, Origin: store.OriginNative,
	}})
	if err != nil || len(results) != 1 || (results[0].Status != "applied" && results[0].Status != "duplicate") {
		s.readingStatusDone(w, r, bookID, "", "could not save reading status")
		return
	}
	s.readingStatusDone(w, r, bookID, notice, "")
}

// markWorkForBook returns the reader's existing work or creates the same
// catalog-backed work that the API resolve route would create on first open.
func (s *Server) markWorkForBook(
	r *http.Request, userID string, book store.CatalogBook,
) (string, error) {
	link, err := s.St.UserBookWork(r.Context(), userID, book.ID)
	switch {
	case err == nil:
		return link.WorkID, nil
	case !errors.Is(err, store.ErrNotFound):
		return "", err
	}

	bookIDs, author, err := workident.Evidence(r.Context(), s.St, book.ID)
	if err != nil {
		return "", err
	}
	workID, err := s.generateSecret()
	if err != nil {
		return "", err
	}
	proposed, editions, ids := workident.Plan(userID, workID, book, bookIDs, author)
	proposed.CreatedAt = time.Now().UTC()
	result, err := s.St.ResolveCatalogBookWork(
		r.Context(), userID, book.ID, proposed, editions, ids, true, proposed.CreatedAt,
	)
	if err != nil {
		return "", err
	}
	if result.WorkID == "" {
		return "", store.ErrConflict
	}
	return result.WorkID, nil
}

func (s *Server) readingStatusDone(
	w http.ResponseWriter, r *http.Request, bookID, notice, problem string,
) {
	back := relPrefix(r.URL.Path) + "books/" + url.PathEscape(bookID)
	s.deleteDone(w, back, notice, problem)
}
