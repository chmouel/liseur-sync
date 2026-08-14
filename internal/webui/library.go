package webui

// The library page: one wall of covers for everything, whether this
// server holds the file, has only watched somebody read it, or both.
//
// Before this there were two pages. /ui/books was the catalog — what
// this server holds — and /ui/works was reading — what has been read.
// The same book appeared on both and neither knew about the other, so a
// reader had to know which of the two words meant "my books". The
// Liseur app has never had that problem: it has one screen, with a
// continue-reading shortcut at the top and filter chips over a single
// grid. This is that screen.
//
// The merge is a union rather than a join, because both halves have
// rows the other cannot produce:
//
//   - a book with no work has never been opened, which is the normal
//     state of a freshly filled library;
//   - a work with no book is progress synced from a device holding a
//     file this server never saw, so it has no cover and nothing to
//     open — and dropping it would lose the only record that somebody
//     read that book.

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The filters, which are the app's chips plus one. "Here" is the app's
// "Downloaded" seen from the other end of the sync: on a server, the
// question is not whether the file came down but whether it is up.
const (
	filterAll      = "all"
	filterReading  = "reading"
	filterUnread   = "unread"
	filterFinished = "finished"
	filterHere     = "here"
)

// The sorts. Alphabetical is missing on purpose: the catalog is paged
// with a (created_at, id) cursor, and sorting by title needs a second
// ordering in the store rather than a re-sort of whatever page happened
// to arrive. It is a follow-up, not a footnote.
const (
	sortRecent   = "recent"
	sortLastRead = "read"
)

// finished is where a progression stops being "reading". It is not 1.0
// because no reader ever lands exactly on the last byte: back matter,
// an index and a colophon all sit past the point a book is over.
const finished = 0.999

// LibraryRow is one entry on the wall: a book, a work, or — most often —
// both, since a book that has been opened is one row and not two.
//
// Which identifiers are set is the whole meaning of the row. BookID
// present means this server holds it (cover, download, reader);
// WorkID present means somebody has read it (progress, statistics).
type LibraryRow struct {
	BookID string
	WorkID string
	Title  string
	Author string
	// Added is when the catalog got it, LastActive when it was last
	// read. Both are already formatted in the user's timezone.
	Added       string
	LastActive  string
	Progression *float64
	// Pending marks a work whose device has sent progress this server
	// has not yet reconciled.
	Pending bool
	// CanRead is an EPUB the browser can open; CanGet is any available
	// file. A PDF is downloadable and not readable here.
	CanRead bool
	CanGet  bool
	// lastActiveAt is the unformatted time, kept for sorting only.
	lastActiveAt int64
}

// Started reports a row somebody has begun and not finished — the rows
// the continue-reading hero and the Reading chip are made of.
func (r LibraryRow) Started() bool {
	return r.Progression != nil && *r.Progression > 0 && *r.Progression < finished
}

// Finished reports a row read to the end.
func (r LibraryRow) Finished() bool {
	return r.Progression != nil && *r.Progression >= finished
}

// href is where the cover goes. Carrying on reading is what somebody
// clicking their own shelf almost always means, so that wins when it is
// possible; otherwise the row goes wherever it has something to say.
func (r LibraryRow) href(prefix string) string {
	switch {
	case r.CanRead && r.BookID != "":
		return prefix + "books/" + r.BookID + "/read"
	case r.BookID != "":
		return prefix + "books/" + r.BookID
	default:
		return prefix + "works/" + r.WorkID
	}
}

// detailURL is the page that holds everything this row can do: the
// book when this server has one, the work's statistics when all it has
// is a record of somebody reading it. The card's open button goes here
// rather than growing a menu of its own.
func (r LibraryRow) detailURL(prefix string) string {
	if r.BookID != "" {
		return prefix + "books/" + r.BookID
	}
	return prefix + "works/" + r.WorkID
}

// hint says what the cover will do, since it does several different
// things depending on what exists behind the row.
func (r LibraryRow) hint() string {
	switch {
	case r.CanRead && r.BookID != "" && r.Started():
		return "Continue reading " + orPlaceholder(r.Title)
	case r.CanRead && r.BookID != "":
		return "Read " + orPlaceholder(r.Title)
	default:
		return orPlaceholder(r.Title)
	}
}

// FilterChip is one of the chips over the grid.
type FilterChip struct {
	Key    string
	Label  string
	URL    string
	Active bool
}

type LibraryView struct {
	Libraries []LibraryOption
	Selected  string
	CanWrite  bool
	Rows      []LibraryRow
	// Hero is the one book to carry on with, or nil when there is
	// nothing started. It is a shortcut rather than a section: it
	// repeats a row that is also in the grid.
	Hero    *LibraryRow
	Chips   []FilterChip
	Filter  string
	Sort    string
	SortURL string
	// The librarian's queues, which used to live on the books page and
	// are shown to nobody else.
	Uploads    []UploadRow
	Trash      []TrashRow
	Review     []ReviewRow
	Duplicates []DuplicateGroup
	Similar    []SimilarGroup
	NextURL    string
	Notice     string
	Problem    string
	View       string
	Back       string
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	libs, err := s.St.ListLibraries(r.Context(), u.ID, store.LibraryRoleRead)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	v := LibraryView{
		Notice:  r.URL.Query().Get("notice"),
		Problem: r.URL.Query().Get("problem"),
		View:    readPrefs(r).View,
		Filter:  normalizeFilter(r.URL.Query().Get("filter")),
		Sort:    normalizeSort(r.URL.Query().Get("sort")),
	}
	selected := r.URL.Query().Get("library")
	if selected == "" && len(libs) > 0 {
		selected = libs[0].Library.ID
	}
	for _, l := range libs {
		v.Libraries = append(v.Libraries, LibraryOption{
			ID:       l.Library.ID,
			Name:     l.Library.Name,
			CanWrite: l.Role == store.LibraryRoleManage,
			Selected: l.Library.ID == selected,
		})
		if l.Library.ID == selected {
			v.Selected = selected
			v.CanWrite = l.Role == store.LibraryRoleManage
		}
	}
	v.Back = libraryURL(v.Selected, v.Filter, v.Sort, "")
	v.Chips = libraryChips(v.Selected, v.Filter, v.Sort)
	v.SortURL = libraryURL(v.Selected, v.Filter, otherSort(v.Sort), "")

	// A library id from the query string that the user cannot read is
	// treated as no selection at all, rather than as an error: it is
	// most often a stale bookmark. The reading half of the page does
	// not depend on a library, but the catalog half is all there is to
	// show once there is no library to show it from.
	if v.Selected == "" && len(libs) > 0 {
		libraryPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
			Render(r.Context(), w)
		return
	}

	loc := userLoc(u)
	works, err := s.St.ListWorks(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	workBookIDs, _ := s.St.WorkBookIDs(r.Context(), u.ID)
	byBook := make(map[string]store.WorkSummary, len(works))
	for _, ws := range works {
		if id := workBookIDs[ws.Work.ID]; id != "" {
			byBook[id] = ws
		}
	}

	rows, next, err := s.libraryRows(r, u, v, works, workBookIDs, byBook, loc)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	v.Rows = rows
	if next != "" {
		v.NextURL = libraryURL(v.Selected, v.Filter, v.Sort, next)
	}
	v.Hero = s.libraryHero(r, u, works, workBookIDs, loc)

	// An htmx continuation asks for more of one list, not for the page
	// around it: answering with the whole document would append a second
	// copy of the shell to the grid, and would make the librarian's
	// review queries run again for markup nobody is going to look at.
	if isHTMXRequest(r) {
		libraryFragment(relPrefix(r.URL.Path), v).Render(r.Context(), w)
		return
	}
	// Only a librarian uploads, so only a librarian is shown what became
	// of an upload. Without this the page is silent about a file that was
	// accepted and then rejected, which looks exactly like losing it.
	if v.CanWrite {
		v.Uploads = s.uploadActivity(r, u.ID, v.Selected, loc)
		v.Trash = s.trashActivity(r, u.ID, v.Selected, loc)
		// Shown to librarians only, for the same reason as the trash: it
		// is a list of things to do something about, and only a librarian
		// can do anything about them.
		v.Duplicates = s.duplicateGroups(r, u.ID, v.Selected)
		v.Similar = s.similarGroups(r, u.ID, v.Selected, loc)
		// Same audience, same reason: a watched file that changed is a
		// decision waiting for somebody who can make it.
		v.Review = s.reviewRows(r, u.ID, v.Selected)
	}
	libraryPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}

// libraryRows builds the union, and returns the cursor for the next page
// when there is one.
//
// There are two ways through this, because the two halves of the union
// are counted differently. The catalog is paged with a cursor and can be
// enormous; the reading history is complete in memory already and is
// bounded by what one person has actually read. So a filter about
// reading state is answered from the reading half — every match at
// once, no paging — and a filter about the catalog is answered from the
// catalog half, paged. Answering "Reading" from a paged catalog would
// mean clicking "next page" past ninety unread books to find the second
// book you are in the middle of.
func (s *Server) libraryRows(
	r *http.Request, u *store.User, v LibraryView,
	works []store.WorkSummary, workBookIDs map[string]string,
	byBook map[string]store.WorkSummary, loc *time.Location,
) ([]LibraryRow, string, error) {
	if v.Filter == filterReading || v.Filter == filterFinished || v.Sort == sortLastRead {
		return s.readingRows(r, u, v, works, workBookIDs, loc), "", nil
	}

	books, next, err := s.listBooksPage(r, u.ID, v.Selected)
	if err != nil {
		return nil, "", err
	}
	bookIDs := make([]string, 0, len(books))
	for _, b := range books {
		bookIDs = append(bookIDs, b.ID)
	}
	authors, _ := s.St.CatalogAuthorsForBooks(r.Context(), u.ID, bookIDs)
	media, _ := s.St.AvailableBookMediaTypes(r.Context(), u.ID, bookIDs)

	rows := make([]LibraryRow, 0, len(books))
	for _, b := range books {
		row := LibraryRow{
			BookID: b.ID,
			Title:  b.Title,
			Author: authors[b.ID],
			Added:  b.CreatedAt.In(loc).Format("Jan 2, 2006"),
		}
		for _, mt := range media[b.ID] {
			row.CanGet = true
			row.CanRead = row.CanRead || isEPUB(mt)
		}
		if ws, ok := byBook[b.ID]; ok {
			row.WorkID = ws.Work.ID
			row.Progression = ws.Progression
			row.Pending = ws.Pending
			if ws.Work.Author != "" && row.Author == "" {
				row.Author = ws.Work.Author
			}
			if ws.LastActive != nil {
				row.LastActive = ws.LastActive.In(loc).Format("Jan 2")
				row.lastActiveAt = ws.LastActive.Unix()
			}
		}
		if !keepRow(row, v.Filter) {
			continue
		}
		rows = append(rows, row)
	}

	// Works with no book belong to the whole shelf, not to a library, so
	// they cannot be paged with one. They ride on the first page, ahead
	// of the catalog: they are all things somebody has read, which is
	// more interesting than the next twenty-five books nobody has
	// opened. Repeating them on every page would be the alternative,
	// and it would be a bug.
	if v.Filter == filterAll && r.URL.Query().Get("cursor") == "" {
		rows = append(orphanRows(works, workBookIDs, loc), rows...)
	}
	return rows, next, nil
}

// readingRows answers the reading-state filters from the reading half of
// the union: every work, whether or not this server holds its file,
// newest read first.
func (s *Server) readingRows(
	r *http.Request, u *store.User, v LibraryView,
	works []store.WorkSummary, workBookIDs map[string]string, loc *time.Location,
) []LibraryRow {
	rows := make([]LibraryRow, 0, len(works))
	for _, ws := range works {
		row := workRowOf(ws, workBookIDs[ws.Work.ID], loc)
		if !keepRow(row, v.Filter) {
			continue
		}
		rows = append(rows, row)
	}
	rows = s.markLibraryReadable(r, u.ID, rows)
	// A filter about the catalog, answered from reading data, still has
	// to mean what it says on the chip.
	if v.Filter == filterHere {
		kept := rows[:0]
		for _, row := range rows {
			if row.CanGet || row.CanRead {
				kept = append(kept, row)
			}
		}
		rows = kept
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].lastActiveAt > rows[j].lastActiveAt
	})
	return rows
}

// orphanRows is the works this server holds no file for. They render as
// text tiles, because there is no cover to show and inventing one would
// be pretending the file is here.
func orphanRows(
	works []store.WorkSummary, workBookIDs map[string]string, loc *time.Location,
) []LibraryRow {
	rows := make([]LibraryRow, 0)
	for _, ws := range works {
		if workBookIDs[ws.Work.ID] != "" {
			continue
		}
		rows = append(rows, workRowOf(ws, "", loc))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].lastActiveAt > rows[j].lastActiveAt
	})
	return rows
}

func workRowOf(ws store.WorkSummary, bookID string, loc *time.Location) LibraryRow {
	row := LibraryRow{
		BookID:      bookID,
		WorkID:      ws.Work.ID,
		Title:       ws.Work.Title,
		Author:      ws.Work.Author,
		Progression: ws.Progression,
		Pending:     ws.Pending,
	}
	if ws.LastActive != nil {
		row.LastActive = ws.LastActive.In(loc).Format("Jan 2")
		row.lastActiveAt = ws.LastActive.Unix()
	}
	return row
}

// keepRow is what each chip means, in one place, so the grid and the
// counts can never disagree about it.
func keepRow(row LibraryRow, filter string) bool {
	switch filter {
	case filterReading:
		return row.Started()
	case filterFinished:
		return row.Finished()
	case filterUnread:
		return row.Progression == nil || *row.Progression <= 0
	case filterHere:
		return row.CanGet || row.CanRead
	default:
		return true
	}
}

// libraryHero picks the book to carry on with: the most recently read
// unfinished work whose file is here to open. It ignores the filter,
// because it is a shortcut back to what you were doing rather than a
// view of the list below it.
func (s *Server) libraryHero(
	r *http.Request, u *store.User,
	works []store.WorkSummary, workBookIDs map[string]string, loc *time.Location,
) *LibraryRow {
	best := LibraryRow{}
	found := false
	for _, ws := range works {
		bookID := workBookIDs[ws.Work.ID]
		if bookID == "" || ws.LastActive == nil {
			continue
		}
		row := workRowOf(ws, bookID, loc)
		if !row.Started() {
			continue
		}
		if !found || row.lastActiveAt > best.lastActiveAt {
			best, found = row, true
		}
	}
	if !found {
		return nil
	}
	// A hero with no Read button is a picture of a book and a dead end,
	// so it only exists when the file is here and openable.
	rows := s.markLibraryReadable(r, u.ID, []LibraryRow{best})
	if !rows[0].CanRead {
		return nil
	}
	return &rows[0]
}

// markLibraryReadable asks once for the whole page which of these books
// hold a file, rather than once per row: a wall of a hundred covers
// would otherwise be a hundred queries to draw.
func (s *Server) markLibraryReadable(r *http.Request, userID string, rows []LibraryRow) []LibraryRow {
	ids := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.BookID == "" || seen[row.BookID] {
			continue
		}
		seen[row.BookID] = true
		ids = append(ids, row.BookID)
	}
	if len(ids) == 0 {
		return rows
	}
	types, err := s.St.AvailableBookMediaTypes(r.Context(), userID, ids)
	if err != nil {
		return rows
	}
	for i := range rows {
		for _, mt := range types[rows[i].BookID] {
			rows[i].CanGet = true
			rows[i].CanRead = rows[i].CanRead || isEPUB(mt)
		}
	}
	return rows
}

func normalizeFilter(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case filterReading:
		return filterReading
	case filterUnread:
		return filterUnread
	case filterFinished:
		return filterFinished
	case filterHere:
		return filterHere
	default:
		return filterAll
	}
}

func normalizeSort(raw string) string {
	if strings.ToLower(strings.TrimSpace(raw)) == sortLastRead {
		return sortLastRead
	}
	return sortRecent
}

func otherSort(current string) string {
	if current == sortLastRead {
		return sortRecent
	}
	return sortLastRead
}

func sortLabel(current string) string {
	if current == sortLastRead {
		return "Last read"
	}
	return "Recently added"
}

// libraryURL keeps the whole state of the page in its URL, so a filtered
// shelf is something you can send to somebody and an htmx continuation
// asks for more of what is on screen rather than more of the default.
func libraryURL(library, filter, sortBy, cursor string) string {
	q := url.Values{}
	if library != "" {
		q.Set("library", library)
	}
	if filter != "" && filter != filterAll {
		q.Set("filter", filter)
	}
	if sortBy != "" && sortBy != sortRecent {
		q.Set("sort", sortBy)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if len(q) == 0 {
		return "library"
	}
	return "library?" + q.Encode()
}

func libraryChips(library, filter, sortBy string) []FilterChip {
	defs := []struct{ key, label string }{
		{filterAll, "All"},
		{filterReading, "Reading"},
		{filterUnread, "Unread"},
		{filterFinished, "Finished"},
		{filterHere, "On this server"},
	}
	chips := make([]FilterChip, 0, len(defs))
	for _, d := range defs {
		chips = append(chips, FilterChip{
			Key:    d.key,
			Label:  d.label,
			URL:    libraryURL(library, d.key, sortBy, ""),
			Active: d.key == filter,
		})
	}
	return chips
}
