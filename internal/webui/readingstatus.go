package webui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/workident"
)

// handleBookReadingStatus records the reader's explicit read/unread choice
// as a status act beside the position (ADR-0066), so it is private to the
// reader and syncs to their other devices like any progress update. The
// act carries the reader's own progression: marking a book read is a
// statement about the book, not a jump to the last page.
func (s *Server) handleBookReadingStatus(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	bookID := r.PathValue("id")
	book, err := s.St.CatalogBookByID(r.Context(), u.ID, bookID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.readingStatusDone(w, r, bookID, "", "could not load the book")
		return
	}

	status, notice := store.ReadingStatusFinished, "book marked as read"
	switch r.FormValue("status") {
	case "read":
	case "unread":
		status, notice = store.ReadingStatusUnread, "book marked as unread"
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
	var editionSHA *string
	if book.ContentSHA256 != "" {
		sha := book.ContentSHA256
		editionSHA = &sha
	}
	ops, err := s.readingStatusOps(r.Context(), u.ID, workID, editionSHA, status)
	if err != nil {
		s.readingStatusDone(w, r, bookID, "", "could not save reading status")
		return
	}
	results, err := s.St.AppendOps(r.Context(), u.ID, "web-ui", ops)
	if err != nil || !appliedOrDuplicate(results) {
		s.readingStatusDone(w, r, bookID, "", "could not save reading status")
		return
	}
	s.readingStatusDone(w, r, bookID, notice, "")
}

// readingStatusOps builds the write behind one toggle: the status act,
// which carries the reader's current position, and — for unread — the
// position reset that rides in the same batch, because the act says what
// the reader decided and the reset is where their devices resume from
// (ADR-0066).
func (s *Server) readingStatusOps(
	ctx context.Context, userID, workID string, editionSHA *string, status store.ReadingStatus,
) ([]store.Op, error) {
	mint := func() (store.Op, error) {
		id, err := s.generateSecret()
		if err != nil {
			return store.Op{}, err
		}
		return store.Op{
			UserID: userID, OpID: id, WorkID: workID, EditionSHA: editionSHA,
			ClientTS: time.Now().UTC(), Origin: store.OriginNative,
		}, nil
	}
	act, err := mint()
	if err != nil {
		return nil, err
	}
	act.Kind = store.OpKindStatus
	act.ReadingStatus = status
	if ops, err := s.St.Positions(ctx, userID, workID, 1); err == nil && len(ops) > 0 {
		act.Progression = ops[0].Progression
	}
	if status != store.ReadingStatusUnread {
		return []store.Op{act}, nil
	}
	reset, err := mint()
	if err != nil {
		return nil, err
	}
	return []store.Op{reset, act}, nil
}

// readingChoice is the newest reading-status act on a book's work, or
// empty when the reader never made one. A book with no work behind it
// has no history to act on.
func (s *Server) readingChoice(r *http.Request, userID, bookID string) string {
	link, err := s.St.UserBookWork(r.Context(), userID, bookID)
	if err != nil {
		return ""
	}
	readings, err := s.St.WorkReadings(r.Context(), userID, []string{link.WorkID})
	if err != nil {
		return ""
	}
	return string(readings[link.WorkID].Choice)
}

// appliedOrDuplicate reports whether a batch was stored: every item was
// new or already there.
func appliedOrDuplicate(results []store.OpResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r.Status != "applied" && r.Status != "duplicate" {
			return false
		}
	}
	return true
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

	bookIDs, author, err := workident.Evidence(r.Context(), s.St, userID, book.ID)
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
