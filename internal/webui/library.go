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
	"context"
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

// The sort direction. desc is the default for both fields: newest
// added, or most recently read, first.
const (
	sortDirDesc = "desc"
	sortDirAsc  = "asc"
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
	// Bookless is a work no catalog book maps at all, as against one
	// whose book is merely missing. Only a bookless work is the
	// reader's to delete: a missing book is evidence about a disk, and
	// the disk comes back (ADR-0024).
	Bookless bool
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
	Folders  []FolderOption
	Selected string
	Rows     []LibraryRow
	// Entries is the Android-style mixed grid: a standalone book or a
	// whole series pile. Rows remains the ungrouped source and the list
	// view, which deliberately stays one row per book.
	Entries     []LibraryEntry
	GroupSeries bool
	// Hero is the one book to carry on with, or nil when there is
	// nothing started. It is a shortcut rather than a section: it
	// repeats a row that is also in the grid.
	Hero    *LibraryRow
	Chips   []FilterChip
	Filter  string
	Sort    string
	SortURL string
	Dir     string
	DirURL  string
	NextURL string
	Notice  string
	Problem string
	View    string
	Back    string
	// CanUpload is the selected folder taking uploads, which is the
	// only thing that puts the send-a-book form on the page. It is the
	// administrator's decision (ADR-0023), read back rather than
	// guessed at, so a reader is never offered a form that would be
	// refused.
	CanUpload bool
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	folders, err := s.St.ListFolders(r.Context(), "", folderPickerLimit)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	p := readPrefs(r)
	v := LibraryView{
		Notice:      r.URL.Query().Get("notice"),
		Problem:     r.URL.Query().Get("problem"),
		View:        p.View,
		GroupSeries: p.GroupSeries,
		Filter:      normalizeFilter(r.URL.Query().Get("filter")),
		Sort:        normalizeSort(r.URL.Query().Get("sort")),
		Dir:         normalizeDir(r.URL.Query().Get("dir")),
	}
	selected := r.URL.Query().Get("folder")
	if selected == "" && len(folders) > 0 {
		selected = folders[0].ID
	}
	for _, f := range folders {
		v.Folders = append(v.Folders, FolderOption{
			ID: f.ID, Name: f.Name, Selected: f.ID == selected,
		})
		if f.ID == selected {
			v.Selected = selected
			v.CanUpload = f.AcceptsUploads && s.Uploads != nil
		}
	}
	v.Back = libraryURL(v.Selected, v.Filter, v.Sort, v.Dir, "")
	v.Chips = libraryChips(v.Selected, v.Filter, v.Sort, v.Dir)
	// Switching field resets direction to the default: a field's
	// natural order is not the other field's reversal.
	v.SortURL = libraryURL(v.Selected, v.Filter, otherSort(v.Sort), "", "")
	v.DirURL = libraryURL(v.Selected, v.Filter, v.Sort, otherDir(v.Dir), "")

	// A folder id from the query string that no longer exists is
	// treated as no selection at all, rather than as an error: it is
	// most often a stale bookmark. The reading half of the page does
	// not depend on a folder, but the catalog half is all there is to
	// show once there is no folder to show it from.
	if v.Selected == "" && len(folders) > 0 {
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
	links := s.workBookIDs(r.Context(), u.ID, works)
	byBook := make(map[string]store.WorkSummary, len(works))
	for _, ws := range works {
		if id := links.active[ws.Work.ID]; id != "" {
			byBook[id] = ws
		}
	}

	rows, entries, next, err := s.libraryRows(r, u.ID, v, works, links, byBook, loc)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	v.Rows = rows
	v.Entries = entries
	if next != "" {
		v.NextURL = libraryURL(v.Selected, v.Filter, v.Sort, v.Dir, next)
	}
	v.Hero = s.libraryHero(r, works, links, loc)

	// An htmx continuation asks for more of one list, not for the page
	// around it: answering with the whole document would append a second
	// copy of the shell to the grid.
	if isHTMXRequest(r) {
		libraryFragment(relPrefix(r.URL.Path), csrfFor(a), v).Render(r.Context(), w)
		return
	}
	libraryPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}

// folderPickerLimit bounds the picker at the top of the page. A server
// watching more folders than this has a navigation problem no <select>
// solves, and the list is read on every render.
const folderPickerLimit = 200

// workBookIDs maps each of the caller's works to the catalog book it was
// resolved from, where there is one.
//
// It is one lookup per work, because the store answers this question for
// a single work at a time. The union this page draws needs the whole map
// at once — which shelf row is which work, and which works have no book
// on this server at all — so the mismatch is paid for here rather than
// hidden in a shape the store does not have.
// workBookIDs answers "which book on this server is this work", and
// answers it only with a book this server can actually serve. A work
// whose only book is missing is treated as having no book here, so it
// renders as a text tile through orphanRows rather than as a cover that
// resolves to a 410. The mapping itself is untouched: the file coming
// back restores the tile.
// The second answer is the one deletion needs: whether the catalog
// lists any book for this work at all, missing file or not. A work
// whose book is only missing looks bookless on this page, and must not
// be offered a delete — the row is the record of a disk, and the disk
// comes back.
func (s *Server) workBookIDs(
	ctx context.Context, userID string, works []store.WorkSummary,
) bookLinks {
	candidates := make(map[string][]string, len(works))
	var all []string
	for _, ws := range works {
		ids, err := s.St.WorkBookIDs(ctx, userID, ws.Work.ID)
		if err != nil || len(ids) == 0 {
			continue
		}
		candidates[ws.Work.ID] = ids
		all = append(all, ids...)
	}
	books := s.booksByID(ctx, all)
	links := bookLinks{
		active: make(map[string]string, len(candidates)),
		mapped: make(map[string]bool, len(candidates)),
	}
	for workID, ids := range candidates {
		links.mapped[workID] = true
		for _, id := range ids {
			if book, ok := books[id]; ok && book.Status == store.BookActive {
				links.active[workID] = id
				break
			}
		}
	}
	return links
}

// bookLinks is what one reader's works resolve to on this server: the
// book each work opens from, and whether the catalog lists a book for
// it at all. The two differ for a work whose only book is missing, and
// that difference decides what a row may do.
type bookLinks struct {
	active map[string]string
	mapped map[string]bool
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
	r *http.Request, userID string, v LibraryView,
	works []store.WorkSummary, links bookLinks,
	byBook map[string]store.WorkSummary, loc *time.Location,
) ([]LibraryRow, []LibraryEntry, string, error) {
	if v.Filter == filterReading || v.Filter == filterFinished || v.Sort == sortLastRead {
		rows := s.readingRows(r, v, works, links, loc)
		if !v.groupedGrid() {
			return rows, nil, "", nil
		}
		entries, err := s.groupedReadingEntries(
			r, userID, v, rows, byBook, loc)
		return rows, entries, "", err
	}

	books, next, err := s.listBooksPage(r, v.Selected, v.Dir)
	if err != nil {
		return nil, nil, "", err
	}
	bookIDs := make([]string, 0, len(books))
	for _, b := range books {
		bookIDs = append(bookIDs, b.ID)
	}
	authors, _ := s.St.CatalogAuthorsForBooks(r.Context(), bookIDs)

	raw := make([]LibraryRow, 0, len(books))
	for _, b := range books {
		row := LibraryRow{
			BookID: b.ID,
			Title:  b.Title,
			Author: credit(authors[b.ID]),
			Added:  b.CreatedAt.In(loc).Format("Jan 2, 2006"),
			// The row already holds the book, so what it can do is read
			// off it rather than asked for: one file, one media type,
			// and a status that says whether a scan can still find it.
			CanGet:  b.Status == store.BookActive,
			CanRead: bookReadable(b),
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
		raw = append(raw, row)
	}
	rows := make([]LibraryRow, 0, len(raw))
	for _, row := range raw {
		if keepRow(row, v.Filter) {
			rows = append(rows, row)
		}
	}
	var entries []LibraryEntry
	if v.groupedGrid() {
		entries, err = s.groupedCatalogEntries(
			r, userID, v, raw, byBook, loc)
		if err != nil {
			return nil, nil, "", err
		}
	}

	// Works with no book belong to the whole shelf, not to a folder, so
	// they cannot be paged with one. They ride on the first page, ahead
	// of the catalog: they are all things somebody has read, which is
	// more interesting than the next twenty-five books nobody has
	// opened. Repeating them on every page would be the alternative,
	// and it would be a bug.
	if v.Filter == filterAll && r.URL.Query().Get("cursor") == "" {
		orphans := orphanRows(works, links, loc)
		rows = append(orphans, rows...)
		if v.groupedGrid() {
			entries = append(rowEntries(orphans), entries...)
		}
	}
	return rows, entries, next, nil
}

func (v LibraryView) groupedGrid() bool {
	return v.View == viewGrid && v.GroupSeries
}

// readingRows answers the reading-state filters from the reading half of
// the union: every work, whether or not this server holds its file,
// newest read first.
func (s *Server) readingRows(
	r *http.Request, v LibraryView,
	works []store.WorkSummary, links bookLinks, loc *time.Location,
) []LibraryRow {
	rows := make([]LibraryRow, 0, len(works))
	for _, ws := range works {
		row := workRowOf(ws, links.active[ws.Work.ID], loc)
		row.Bookless = !links.mapped[ws.Work.ID]
		if !keepRow(row, v.Filter) {
			continue
		}
		rows = append(rows, row)
	}
	rows = s.markLibraryReadable(r, rows)
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
		if v.Dir == sortDirAsc {
			return rows[i].lastActiveAt < rows[j].lastActiveAt
		}
		return rows[i].lastActiveAt > rows[j].lastActiveAt
	})
	return rows
}

// orphanRows is the works this server holds no file for. They render as
// text tiles, because there is no cover to show and inventing one would
// be pretending the file is here.
func orphanRows(
	works []store.WorkSummary, links bookLinks, loc *time.Location,
) []LibraryRow {
	rows := make([]LibraryRow, 0)
	for _, ws := range works {
		if links.active[ws.Work.ID] != "" {
			continue
		}
		row := workRowOf(ws, "", loc)
		row.Bookless = !links.mapped[ws.Work.ID]
		rows = append(rows, row)
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
	r *http.Request,
	works []store.WorkSummary, links bookLinks, loc *time.Location,
) *LibraryRow {
	best := LibraryRow{}
	found := false
	for _, ws := range works {
		bookID := links.active[ws.Work.ID]
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
	rows := s.markLibraryReadable(r, []LibraryRow{best})
	if !rows[0].CanRead {
		return nil
	}
	return &rows[0]
}

// markLibraryReadable fills in what each row's book can do. Rows built
// from a catalog listing already know — the book is in hand there — so
// this is for the rows that came from the reading half of the union,
// where all that is known is which book a work was resolved from.
func (s *Server) markLibraryReadable(r *http.Request, rows []LibraryRow) []LibraryRow {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.BookID != "" {
			ids = append(ids, row.BookID)
		}
	}
	books := s.booksByID(r.Context(), ids)
	for i := range rows {
		book, ok := books[rows[i].BookID]
		if !ok {
			continue
		}
		rows[i].CanGet = book.Status == store.BookActive
		rows[i].CanRead = bookReadable(book)
	}
	return rows
}

// normalizeFilter is the chip a request is asking for.
//
// Asking for nothing is the landing view, and that is "on this server"
// rather than everything: "all" also carries the works this server holds
// no file for, and a wall of coverless text tiles is a poor first
// impression of a library. They are one chip away, which is where a
// thing that is not really on the shelf belongs.
//
// A name that is not a chip is still everything, though. That is the one
// case where the wrong answer should be the generous one: a mistyped or
// stale link should not quietly hide books.
func normalizeFilter(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return filterHere
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

func normalizeDir(raw string) string {
	if strings.ToLower(strings.TrimSpace(raw)) == sortDirAsc {
		return sortDirAsc
	}
	return sortDirDesc
}

func otherDir(current string) string {
	if current == sortDirAsc {
		return sortDirDesc
	}
	return sortDirAsc
}

func dirLabel(current string) string {
	if current == sortDirAsc {
		return "Oldest first"
	}
	return "Newest first"
}

func dirArrow(current string) string {
	if current == sortDirAsc {
		return "↑"
	}
	return "↓"
}

// libraryURL keeps the whole state of the page in its URL, so a filtered
// shelf is something you can send to somebody and an htmx continuation
// asks for more of what is on screen rather than more of the default.
func libraryURL(folder, filter, sortBy, dir, cursor string) string {
	q := url.Values{}
	if folder != "" {
		q.Set("folder", folder)
	}
	if filter != "" && filter != filterAll {
		q.Set("filter", filter)
	}
	if sortBy != "" && sortBy != sortRecent {
		q.Set("sort", sortBy)
	}
	if dir != "" && dir != sortDirDesc {
		q.Set("dir", dir)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if len(q) == 0 {
		return "library"
	}
	return "library?" + q.Encode()
}

func libraryChips(folder, filter, sortBy, dir string) []FilterChip {
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
			URL:    libraryURL(folder, d.key, sortBy, dir, ""),
			Active: d.key == filter,
		})
	}
	return chips
}
