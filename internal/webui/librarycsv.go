package webui

// The CSV export: the shelf as one flat table.
//
// It is the same union the page draws — a book nobody has opened, a work
// whose file lives somewhere else, and the ordinary row that is both —
// built by the same code, so an export can never disagree with the
// shelf it was taken from. What it adds is the whole list: the page
// pages at twenty-five, and a file somebody opens in a spreadsheet is
// not a page.
//
// The columns are the list view's, spelled for a table rather than for a
// card: dates as YYYY-MM-DD in the reader's own timezone (sortable, and
// unambiguous across an ocean), progress as a bare percentage (a number
// a spreadsheet can average), and the book id last for anyone who wants
// to follow a row back to the API.

import (
	"encoding/csv"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/reading"
	"github.com/chmouel/liseur-sync/internal/store"
)

// csvColumns is the header row, in the order [csvRecord] writes.
var csvColumns = []string{
	"title", "author", "status", "progress", "last_read", "added", "book_id",
}

// csvDate is the one date format the export uses. It is not the card's
// "Jan 2, 2006": a reader sorting a column of those gets January first.
const csvDate = "2006-01-02"

func (s *Server) handleLibraryCSV(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	folder := r.URL.Query().Get("folder")
	if folder != "" {
		// FolderByID is the grant check: a folder this reader holds
		// nothing in is a 404 here exactly as it is on the page, so the
		// export cannot be used to ask about somebody else's shelf.
		if _, err := s.St.FolderByID(r.Context(), u.ID, folder); err != nil {
			http.NotFound(w, r)
			return
		}
	} else {
		// An address that names no folder means the same thing here as
		// it does on the page: the first one this reader holds.
		folders, err := s.St.ListFolders(r.Context(), u.ID, "", 1)
		if err != nil {
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		if len(folders) > 0 {
			folder = folders[0].ID
		}
	}
	v := LibraryView{
		Selected: folder,
		Filter:   normalizeFilter(r.URL.Query().Get("filter")),
		Sort:     normalizeSort(r.URL.Query().Get("sort")),
		Dir:      normalizeDir(r.URL.Query().Get("dir")),
	}
	rows, err := s.exportRows(r, u, v)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	// Nothing is written until every row is in hand, because a store
	// failure halfway down would leave a truncated file that looks
	// complete: there is no place left to put an error once the body
	// has begun.
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="library.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	cw := csv.NewWriter(w)
	_ = cw.Write(csvColumns)
	loc := userLoc(u)
	for _, row := range rows {
		_ = cw.Write(csvRecord(row, loc))
	}
	cw.Flush()
}

// exportRows walks the same cursor the page's "Next page" link would,
// collecting every row the shelf would show across all of its pages.
func (s *Server) exportRows(
	r *http.Request, u *store.User, v LibraryView,
) ([]LibraryRow, error) {
	src, err := s.librarySource(r, u)
	if err != nil {
		return nil, err
	}
	var rows []LibraryRow
	for cursor := ""; ; {
		page := withCursor(r, cursor)
		pageRows, _, next, err := s.libraryRows(
			page, u.ID, v, src.works, src.links, src.readings, src.byBook, src.loc)
		if err != nil {
			return nil, err
		}
		rows = append(rows, pageRows...)
		// A cursor that came back unchanged would spin forever on a
		// store bug; the export is not the place to discover one.
		if next == "" || next == cursor {
			return rows, nil
		}
		cursor = next
	}
}

// withCursor is the request the page would have sent for that cursor,
// with everything else — the folder, the chip, the ordering — left as it
// is. The request is cloned rather than edited because the caller still
// needs its own copy afterwards.
func withCursor(r *http.Request, cursor string) *http.Request {
	page := r.Clone(r.Context())
	target := *r.URL
	q := target.Query()
	if cursor == "" {
		q.Del("cursor")
	} else {
		q.Set("cursor", cursor)
	}
	target.RawQuery = q.Encode()
	page.URL = &target
	return page
}

func csvRecord(row LibraryRow, loc *time.Location) []string {
	progress := ""
	if row.Progression != nil {
		progress = strconv.Itoa(int(math.Round(*row.Progression * 100)))
	}
	lastRead := ""
	if row.lastActiveAt > 0 {
		lastRead = time.Unix(row.lastActiveAt, 0).In(loc).Format(csvDate)
	}
	added := ""
	if !row.addedAt.IsZero() {
		added = row.addedAt.In(loc).Format(csvDate)
	}
	return []string{
		row.Title, row.Author, csvStatusLabel(row.resolved()), progress,
		lastRead, added, row.BookID,
	}
}

// csvStatusLabel names a resolved status the way the shelf's chips do.
// The wire values ("did_not_finish") are right in JSON and wrong in a
// file somebody reads.
func csvStatusLabel(st reading.Status) string {
	switch st {
	case reading.StatusReading:
		return "Reading"
	case reading.StatusFinished:
		return "Finished"
	case reading.StatusWantToRead:
		return "Want to read"
	case reading.StatusUnread:
		return "Unread"
	case reading.StatusPaused:
		return "Paused"
	case reading.StatusDidNotFinish:
		return "Did not finish"
	default:
		return "Not started"
	}
}
