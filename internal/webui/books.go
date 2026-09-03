package webui

// Books handlers: the browser surface of the catalog. All of the byte
// handling is delegated — this file decides what a page shows, not how
// a file is opened or served.
//
// Almost nothing here writes. A folder's contents belong to whoever
// curates it on disk (ADR-0017), so there is no trash and no metadata
// form: the catalog says what the folder says, and the way to change it
// is to change the folder. The one exception is an administrator
// retiring a catalog row whose file a pass already reported missing
// (ADR-0024) — a row leaves, never a file.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Downloader serves a book's bytes for a caller identified some other
// way than by a token — here, by a session cookie. Reusing it keeps the
// media-type allowlist and filename sanitizing in one place.
//
// The user id is part of the call because shared catalog rows are
// visible only through that reader's folder grants (ADR-0027).
type Downloader interface {
	ServeBookDownload(w http.ResponseWriter, r *http.Request, viewerID, bookID string)
}

// booksPageSize keeps the page short enough to read. The UI paginates
// with the same opaque cursor the API hands out.
const booksPageSize = 25

// isHTMXRequest reports whether this is htmx asking for a fragment.
// Nothing about access depends on it — the header is a hint about what
// to render, never about what may be read — so an attacker setting it
// gains a page of markup they were already entitled to.
func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// listBooksPage returns one page of a folder's books and the cursor for
// the next, or "" when this is the last one. dir picks the direction:
// sortDirAsc pages oldest first, anything else (the default) newest
// first.
func (s *Server) listBooksPage(
	r *http.Request, userID, folderID, dir string,
) ([]store.CatalogBook, string, error) {
	after, err := decodeBooksCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		// A mangled cursor restarts at the beginning. There is nothing
		// for a reader to do about it, and an error page would be worse
		// than the first page.
		after = nil
	}
	list := s.St.ListRecentCatalogBooks
	if dir == sortDirAsc {
		list = s.St.ListCatalogBooks
	}
	books, err := list(r.Context(), userID, folderID, after, booksPageSize)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if len(books) < booksPageSize {
		return books, "", nil
	}
	last := books[len(books)-1]
	return books, encodeBooksCursor(store.CatalogBookCursor{
		CreatedAt: last.CreatedAt, ID: last.ID,
	}), nil
}

// booksByID reads a bounded set of catalog books by id. A book is one
// row and one file now, so everything a shelf asks about it — its media
// type, whether the last pass could still find it — is on the row, and
// there is nothing left to batch. The callers are the reading shelves,
// which are bounded by construction; a catalog page already holds the
// rows it needs.
func (s *Server) booksByID(ctx context.Context, userID string, ids []string) map[string]store.CatalogBook {
	out := make(map[string]store.CatalogBook, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, done := out[id]; done {
			continue
		}
		book, err := s.St.CatalogBookByID(ctx, userID, id)
		if err != nil {
			continue
		}
		out[id] = book
	}
	return out
}

func (s *Server) handleBook(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	v, ok := s.bookView(r, u, r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	v.Notice = r.URL.Query().Get("notice")
	v.Problem = r.URL.Query().Get("problem")
	bookPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}

// bookView assembles the book page from one catalog row and its
// relations. The row must be visible through this reader's folder grant.
func (s *Server) bookView(r *http.Request, u *store.User, bookID string) (BookView, bool) {
	book, err := s.St.CatalogBookByID(r.Context(), u.ID, bookID)
	if err != nil {
		return BookView{}, false
	}
	v := BookView{
		ID: book.ID, Title: book.Title, Subtitle: book.Subtitle,
		Description: book.Description, Publisher: book.Publisher,
		Published: book.PublishedDate, FolderID: book.FolderID,
		Added:    book.CreatedAt.In(userLoc(u)).Format("Jan 2, 2006"),
		Filename: book.OriginalFilename,
		// A book whose file the last pass could not find stays in the
		// catalog and stops being downloadable: the row is a record of
		// what the folder held, and offering a button that answers 410
		// would be worse than saying so.
		Present:   book.Status == store.BookActive,
		MediaType: book.MediaType,
		SHA256:    book.ContentSHA256,
	}
	v.CanRead = bookReadable(book)
	if link, err := s.St.UserBookWork(r.Context(), readerID(u), book.ID); err == nil {
		if ops, err := s.St.Positions(r.Context(), readerID(u), link.WorkID, 1); err == nil && len(ops) > 0 {
			v.Progression = &ops[0].Progression
			v.Finished = ops[0].Progression >= finished
		}
	}
	// Retiring a missing row is the one write this page offers, and
	// only where it would stick: a Calibre folder's metadata.db is
	// authoritative, so a book it still lists comes back on the next
	// pass (ADR-0022, ADR-0024).
	// Deleting a book outright is the other write, and bounded by the
	// flag that bounds uploading: a folder nobody marked as accepting
	// one is still read-only to this server (ADR-0023, ADR-0025). Admin
	// rather than a scope, because a browser session carries no scopes
	// — the account carries the role.
	if isAdmin(r) {
		if folder, err := s.St.FolderByID(r.Context(), u.ID, book.FolderID); err == nil {
			if !v.Present {
				v.Retirable = folder.Kind != store.FolderCalibre
			}
			v.Deletable = v.Present && folder.AcceptsUploads && s.Deletes != nil
		}
	}
	if rel, err := s.St.CatalogBookRelationsForBooks(
		r.Context(), readerID(u), []string{book.ID},
	); err == nil {
		v.Authors, v.Byline = contributorChips(rel.Contributors[book.ID])
		for _, ser := range rel.Series[book.ID] {
			v.Series = append(v.Series, ChipLink{
				Name: ser.Name,
				URL:  uiEntity("series", ser.SeriesID),
			})
		}
	}
	// The organization card states the book's series rather than making
	// a reader open the editor to find out. The layers are read here
	// rather than taken from the relations above because the card also
	// says which layer is in force, and what the folder said underneath
	// a claim (ADR-0018) — neither of which survives into a chip.
	if layers, err := s.St.BookSeriesLayers(
		r.Context(), readerID(u), book.ID,
	); err == nil {
		v.SeriesSource = string(layers.Source)
		for _, ser := range layers.Effective {
			v.SeriesNow = append(v.SeriesNow, BookSeriesLine{
				Name:     ser.Name,
				Position: formatSeriesPosition(ser.Position),
				URL:      uiEntity("series", ser.SeriesID),
			})
		}
		if layers.Source != store.SeriesSourceFolder {
			v.SeriesFolderNote = seriesFolderNote(assignRows(layers.Folder))
		}
	}
	return v, true
}

// contributorChips returns the links and the byline. The byline names
// authors only when the file said who they are, and falls back to every
// contributor when it did not: a book credited solely to a translator
// should still say so rather than say nothing.
func contributorChips(rows []store.BookContributor) ([]ChipLink, string) {
	chips := make([]ChipLink, 0, len(rows))
	var authors []string
	var everyone []string
	seenContributors := make(map[string]struct{}, len(rows))
	seenAuthors := make(map[string]struct{}, len(rows))
	for _, c := range rows {
		key := c.ContributorID
		if key == "" {
			key = c.Name
		}
		if _, seen := seenContributors[key]; !seen {
			chips = append(chips, ChipLink{
				Name: c.Name,
				URL:  uiEntity("contributors", c.ContributorID),
			})
			everyone = append(everyone, c.Name)
			seenContributors[key] = struct{}{}
		}
		if c.Role != "" && !strings.EqualFold(c.Role, store.ContributorRoleAuthor) &&
			!strings.EqualFold(c.Role, "aut") {
			continue
		}
		if _, seen := seenAuthors[key]; seen {
			continue
		}
		seenAuthors[key] = struct{}{}
		authors = append(authors, c.Name)
	}
	if len(authors) == 0 {
		authors = everyone
	}
	return chips, strings.Join(authors, ", ")
}

// handleBookDownload hands off to the API's download, which owns the
// rules about what a stored file may claim to be.
func (s *Server) handleBookDownload(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if s.Downloads == nil {
		http.Error(w, "content storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	s.Downloads.ServeBookDownload(w, r, u.ID, r.PathValue("id"))
}

func encodeBooksCursor(c store.CatalogBookCursor) string {
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID
}

func decodeBooksCursor(raw string) (*store.CatalogBookCursor, error) {
	if raw == "" {
		return nil, nil
	}
	at, id, ok := strings.Cut(raw, "|")
	if !ok || id == "" {
		return nil, errors.New("malformed cursor")
	}
	parsed, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, errors.New("malformed cursor")
	}
	return &store.CatalogBookCursor{CreatedAt: parsed, ID: id}, nil
}

// bookReadable reports whether a catalog row is something the browser
// reader can open: an EPUB the last pass could still find.
func bookReadable(b store.CatalogBook) bool {
	return b.Status == store.BookActive && isEPUB(b.MediaType)
}

// isEPUB reports whether a stored file is something the browser reader
// can open. Everything else in a folder stays downloadable; only EPUB
// is offered for reading, because that is the only format the reader
// knows how to unpack.
func isEPUB(mediaType string) bool {
	return strings.HasPrefix(mediaType, "application/epub")
}
