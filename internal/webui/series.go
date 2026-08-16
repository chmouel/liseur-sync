package webui

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The series pages (ADR-0018). A series is the one entity a reader
// reads *through* rather than merely browses by, so it gets a page of
// its own rather than the generic entity listing: reading order, how far
// this reader got in each volume, what to open next, and the gaps in the
// numbering that say a volume is missing.
//
// It is also the one entity a reader can restate, which is why the
// assign dialog lives here beside the shelf it edits.

// seriesShelfSize bounds one shelf. A series longer than this pages like
// any other listing.
const seriesShelfSize = 200

// seriesSuggestLimit bounds the autocomplete. It is a typeahead, not a
// browser: the entity index is where you go to see them all.
const seriesSuggestLimit = 12

// SeriesShelfView is one series read end to end.
type SeriesShelfView struct {
	SeriesID string
	Name     string
	Notice   string
	Problem  string
	Volumes  []SeriesVolume
	// NextUp is the volume a reader clicking one button wants: the
	// earliest started-but-unfinished one, else the earliest unstarted
	// one. Nil when the series is finished or empty.
	NextUp *SeriesVolume
	// Gaps are the positions the numbering skips, so a shelf can say a
	// volume is missing rather than quietly renumber around it.
	Gaps []string
	// Source names the layer the shelf's memberships came from, and is
	// empty when they are not all from the same one.
	Source  string
	NextURL string
}

// SeriesVolume is one book on the shelf, with this reader's own progress
// against it. The catalog half is shared; the progress half never is.
type SeriesVolume struct {
	BookID   string
	Title    string
	Author   string
	Position string
	// position is the unformatted number, for finding gaps.
	position    *float64
	Progression *float64
	WorkID      string
	CanRead     bool
	CanGet      bool
}

// Started and Finished mirror LibraryRow's, so the shelf and the library
// agree about what a half-read book looks like.
func (v SeriesVolume) Started() bool {
	return v.Progression != nil && *v.Progression > 0 && *v.Progression < finished
}

func (v SeriesVolume) Finished() bool {
	return v.Progression != nil && *v.Progression >= finished
}

// Percent is the progress as whole percent, for the bar and its label.
func (v SeriesVolume) Percent() int {
	if v.Progression == nil {
		return 0
	}
	return int(math.Round(*v.Progression * 100))
}

// handleSeriesShelf renders /ui/entities/series/{entity}. It is
// registered ahead of the generic {kind}/{entity} listing, which still
// serves contributors and tags. A shelf spans folders (ADR-0019), so
// its volumes can come from more than one.
func (s *Server) handleSeriesShelf(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	entity, err := s.St.CatalogEntityByID(
		r.Context(), readerID(u), r.PathValue("entity"), store.EntitySeries)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cursor, err := decodeBooksCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	books, next, err := s.St.ListBooksByEntity(
		r.Context(), readerID(u), entity.ID,
		store.EntitySeries, cursor, seriesShelfSize)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	bookIDs := make([]string, 0, len(books))
	for _, b := range books {
		bookIDs = append(bookIDs, b.ID)
	}
	authors, _ := s.St.CatalogAuthorsForBooks(r.Context(), bookIDs)
	// Positions come from the same resolved relations the book payload
	// uses, so the shelf and the book page never disagree about where a
	// volume sits.
	rel, _ := s.St.CatalogBookRelationsForBooks(r.Context(), readerID(u), bookIDs)

	progress := s.readerProgress(r, u, bookIDs)

	v := SeriesShelfView{
		SeriesID: entity.ID,
		Name:     entity.Name,
		Notice:   r.URL.Query().Get("notice"),
		Problem:  r.URL.Query().Get("problem"),
	}
	sources := map[string]bool{}
	for _, b := range books {
		vol := SeriesVolume{
			BookID: b.ID, Title: b.Title,
			Author:  credit(authors[b.ID]),
			CanGet:  b.Status == store.BookActive,
			CanRead: bookReadable(b),
		}
		for _, ser := range rel.Series[b.ID] {
			if ser.SeriesID != entity.ID {
				continue
			}
			sources[string(ser.Source)] = true
			vol.position = ser.Position
			vol.Position = formatSeriesPosition(ser.Position)
		}
		if p, ok := progress[b.ID]; ok {
			vol.Progression = p.Progression
			vol.WorkID = p.WorkID
		}
		v.Volumes = append(v.Volumes, vol)
	}
	if len(sources) == 1 {
		for src := range sources {
			v.Source = src
		}
	}
	v.NextUp = nextUpVolume(v.Volumes)
	v.Gaps = seriesGaps(v.Volumes)
	if next != nil {
		v.NextURL = "series/" + url.PathEscape(entity.ID) +
			"?cursor=" + url.QueryEscape(encodeBooksCursor(*next))
	}
	seriesShelfPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}

// bookProgress is one book's reading state for this reader.
type bookProgress struct {
	WorkID      string
	Progression *float64
}

// readerProgress maps catalog books to the caller's own reading state.
// It is the caller's works, never anybody else's: the shelf is shared,
// the progress on it is not.
func (s *Server) readerProgress(
	r *http.Request, u *store.User, bookIDs []string,
) map[string]bookProgress {
	out := map[string]bookProgress{}
	if u == nil || len(bookIDs) == 0 {
		return out
	}
	works, err := s.St.ListWorks(r.Context(), u.ID)
	if err != nil {
		return out
	}
	wanted := make(map[string]bool, len(bookIDs))
	for _, id := range bookIDs {
		wanted[id] = true
	}
	for _, ws := range works {
		ids, err := s.St.WorkBookIDs(r.Context(), u.ID, ws.Work.ID)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if wanted[id] {
				out[id] = bookProgress{
					WorkID: ws.Work.ID, Progression: ws.Progression,
				}
				break
			}
		}
	}
	return out
}

// nextUpVolume picks what to open. A volume in progress beats an
// unstarted one, because carrying on is what somebody opening a series
// they are already reading means.
func nextUpVolume(vols []SeriesVolume) *SeriesVolume {
	var unstarted *SeriesVolume
	for i := range vols {
		v := &vols[i]
		if v.Started() {
			return v
		}
		if unstarted == nil && v.Progression == nil && v.CanRead {
			unstarted = v
		}
	}
	return unstarted
}

// seriesGaps names the whole numbers the shelf skips between its first
// and last placed volume. Only whole numbers: 1, 2, 4 is a missing book
// three, but 1, 1.5, 2 is a novella and not a hole.
func seriesGaps(vols []SeriesVolume) []string {
	present := map[int]bool{}
	low, high := 0, 0
	seen := false
	for _, v := range vols {
		if v.position == nil {
			continue
		}
		p := *v.position
		if p != math.Trunc(p) || p < 1 || p > 10000 {
			continue
		}
		n := int(p)
		present[n] = true
		if !seen || n < low {
			low = n
		}
		if !seen || n > high {
			high = n
		}
		seen = true
	}
	if !seen {
		return nil
	}
	var out []string
	for n := low; n <= high; n++ {
		if !present[n] {
			out = append(out, strconv.Itoa(n))
		}
	}
	return out
}

// formatSeriesPosition prints a position the way a reader writes one:
// "3", not "3.0", and "1.5" for a novella between two novels.
func formatSeriesPosition(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

// -------------------------------------------------------------------
// Assigning
// -------------------------------------------------------------------

// SeriesAssignView is the dialog on a book page: what the folder said,
// what has been claimed over it, and what this reader may write.
type SeriesAssignView struct {
	BookID string
	Title  string
	// Current is the effective membership, which is what the form is
	// prefilled from.
	Current []SeriesAssignRow
	// Folder is what the last pass observed, shown so a reader can see
	// what a reset would restore.
	Folder []SeriesAssignRow
	// Source names the layer in force.
	Source string
	// CanReset is false when there is nothing claimed to reset.
	CanReset bool
	// CanShare is an admin: only they write the layer everybody sees.
	CanShare bool
	// Shared is true when the claim in force is the shared one, which a
	// non-admin can see but not change.
	Shared  bool
	Problem string
}

type SeriesAssignRow struct {
	SeriesID string
	Name     string
	Position string
}

func assignRows(list []store.BookSeries) []SeriesAssignRow {
	out := make([]SeriesAssignRow, 0, len(list))
	for _, s := range list {
		out = append(out, SeriesAssignRow{
			SeriesID: s.SeriesID, Name: s.Name,
			Position: formatSeriesPosition(s.Position),
		})
	}
	return out
}

// handleSeriesAssignForm serves the htmx fragment the book page swaps
// in. It is a GET so the dialog costs nothing until it is opened.
func (s *Server) handleSeriesAssignForm(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	v, ok := s.assignView(r, u, r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	v.Problem = r.URL.Query().Get("problem")
	seriesAssignForm(relPrefix(r.URL.Path), csrfFor(a), v).Render(r.Context(), w)
}

func (s *Server) assignView(
	r *http.Request, u *store.User, bookID string,
) (SeriesAssignView, bool) {
	book, err := s.St.CatalogBookByID(r.Context(), bookID)
	if err != nil {
		return SeriesAssignView{}, false
	}
	layers, err := s.St.BookSeriesLayers(r.Context(), readerID(u), bookID)
	if err != nil {
		return SeriesAssignView{}, false
	}
	v := SeriesAssignView{
		BookID: book.ID, Title: book.Title,
		Current:  assignRows(layers.Effective),
		Folder:   assignRows(layers.Folder),
		Source:   string(layers.Source),
		CanShare: u != nil && u.IsAdmin,
		Shared:   layers.Source == store.SeriesSourceShared,
	}
	// A reader can undo their own claim always, and the shared one only
	// if they are the admin who could have written it.
	v.CanReset = layers.Personal != nil ||
		(layers.Shared != nil && v.CanShare)
	return v, true
}

// handleSeriesAssign takes the form. The whole form is the claim: the
// rows left in it are the book's series, and clearing them all says the
// book is in none.
func (s *Server) handleSeriesAssign(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	bookID := r.PathValue("id")
	scope := store.SeriesSourcePersonal
	if r.FormValue("scope") == string(store.SeriesSourceShared) {
		if u == nil || !u.IsAdmin {
			s.assignProblem(w, r, u, a, bookID,
				"Only an administrator changes what everybody sees.")
			return
		}
		scope = store.SeriesSourceShared
	}

	names := r.Form["name"]
	positions := r.Form["position"]
	items := make([]store.SeriesClaimItem, 0, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			// A blank row is how the form says "remove this one", so it
			// is dropped rather than refused.
			continue
		}
		item := store.SeriesClaimItem{Name: name}
		if i < len(positions) {
			raw := strings.TrimSpace(positions[i])
			if raw != "" {
				p, err := strconv.ParseFloat(raw, 64)
				if err != nil || math.IsNaN(p) || math.IsInf(p, 0) {
					s.assignProblem(w, r, u, a, bookID,
						"A position has to be a number, like 3 or 1.5.")
					return
				}
				item.Position = &p
			}
		}
		items = append(items, item)
	}

	if err := s.St.SetBookSeriesOverride(
		r.Context(), readerID(u), bookID, scope, items, time.Now().UTC(),
	); err != nil {
		s.assignProblem(w, r, u, a, bookID, "That could not be saved.")
		return
	}
	s.renderAssign(w, r, u, a, bookID)
}

// handleSeriesReset drops a claim so the book goes back to what the
// folder said, or to the shared claim underneath a personal one.
func (s *Server) handleSeriesReset(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	bookID := r.PathValue("id")
	scope := store.SeriesSourcePersonal
	if r.FormValue("scope") == string(store.SeriesSourceShared) {
		if u == nil || !u.IsAdmin {
			s.assignProblem(w, r, u, a, bookID,
				"Only an administrator changes what everybody sees.")
			return
		}
		scope = store.SeriesSourceShared
	}
	if err := s.St.ClearBookSeriesOverride(
		r.Context(), readerID(u), bookID, scope,
	); err != nil {
		s.assignProblem(w, r, u, a, bookID, "That could not be undone.")
		return
	}
	s.renderAssign(w, r, u, a, bookID)
}

func (s *Server) renderAssign(
	w http.ResponseWriter, r *http.Request,
	u *store.User, a store.AuthSession, bookID string,
) {
	v, ok := s.assignView(r, u, bookID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	seriesAssignForm(relPrefix(r.URL.Path), csrfFor(a), v).Render(r.Context(), w)
}

// assignProblem re-renders the dialog carrying its complaint, rather
// than replacing the page with an error: the reader's typing is still
// on screen and they are one correction away from being done.
func (s *Server) assignProblem(
	w http.ResponseWriter, r *http.Request,
	u *store.User, a store.AuthSession, bookID, problem string,
) {
	v, ok := s.assignView(r, u, bookID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	v.Problem = problem
	w.WriteHeader(http.StatusBadRequest)
	seriesAssignForm(relPrefix(r.URL.Path), csrfFor(a), v).Render(r.Context(), w)
}

// handleSeriesSuggest is the typeahead behind the name field. It offers
// series that already exist so a reader adding a book to one joins it
// rather than founding a near-identical second.
func (s *Server) handleSeriesSuggest(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	// htmx sends the field's own name and value, so the typeahead
	// arrives as `name`; `q` is what a hand-written client would send.
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("name"))
	}
	var out []string
	if q != "" {
		rows, err := s.St.ListCatalogEntities(
			r.Context(), readerID(u), store.EntitySeries, "",
			// The store pages by name and has no prefix search, so the
			// filtering is done here over a bounded page. A library with
			// more series than this needs a store-side search, and the
			// typeahead is not the reason to build one.
			seriesSuggestLimit*40)
		if err == nil {
			needle := strings.ToLower(q)
			for _, row := range rows {
				if !strings.Contains(strings.ToLower(row.Name), needle) {
					continue
				}
				out = append(out, row.Name)
				if len(out) == seriesSuggestLimit {
					break
				}
			}
		}
	}
	seriesSuggestions(out).Render(r.Context(), w)
}

// gapsSentence names the missing volumes in English, because a list of
// bare numbers under a shelf reads as a caption and not as a warning.
func gapsSentence(gaps []string) string {
	if len(gaps) == 1 {
		return "Book " + gaps[0] + " is not in the library."
	}
	return "Books " + joinWithAnd(gaps) + " are not in the library."
}

// folderSeriesSentence says what a reset would restore.
func folderSeriesSentence(rows []SeriesAssignRow) string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Position != "" {
			out = append(out, r.Name+" #"+r.Position)
			continue
		}
		out = append(out, r.Name)
	}
	return joinWithAnd(out)
}

func joinWithAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
