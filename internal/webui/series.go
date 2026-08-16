package webui

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
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
	Source string
	// ScannedName is what the last pass called this series, and
	// NameSource is the layer the displayed name came from — together
	// they are what lets the shelf offer a revert and say what it would
	// revert to (ADR-0020).
	ScannedName string
	NameSource  string
	// CanShare is an admin: only they rename for everybody.
	CanShare bool
	NextURL  string
	// Bindings are the observed names that fold into this shelf because
	// somebody merged or split it (ADR-0021), each one undoable. Filled
	// in for an admin only, since only an admin can act on them.
	Bindings []SeriesBindingRow
	// SplitFolders are the folders whose books are on this shelf. A
	// split is offered only when there is more than one, because moving
	// the only folder's books is a rename.
	SplitFolders []SeriesFolderRow
	// MergeTargetID and MergeTargetName are set when a rename was
	// refused because another shelf holds the name: that refusal is a
	// merge request, so the page offers the merge rather than a dead
	// end.
	MergeTargetID   string
	MergeTargetName string
}

// SeriesBindingRow is one absorbed name as the shelf shows it.
type SeriesBindingRow struct {
	ID string
	// Name is the absorbed name in the spelling it was written in.
	Name string
	// Folder is empty when the binding holds everywhere, which is what
	// a merge writes; a split writes one folder's.
	Folder string
}

// SeriesFolderRow is one folder's contribution, and the unit a split
// moves.
type SeriesFolderRow struct {
	ID    string
	Name  string
	Books int
}

// CanSplit reports whether this shelf holds more than one folder's
// books, which is the only case a folder-wise split addresses.
func (v SeriesShelfView) CanSplit() bool { return len(v.SplitFolders) > 1 }

// CanRevert reports whether this reader can undo the rename in force. A
// reader can always undo their own; the shared one only an admin can,
// which is why a plain reader looking at a curated shelf is offered no
// button that would do nothing.
func (v SeriesShelfView) CanRevert() bool {
	switch v.NameSource {
	case "personal":
		return true
	case "shared":
		return v.CanShare
	}
	return false
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
		SeriesID:    entity.ID,
		Name:        entity.Name,
		ScannedName: entity.ScannedName,
		NameSource:  string(entity.NameSource),
		CanShare:    u != nil && u.IsAdmin,
		Notice:      r.URL.Query().Get("notice"),
		Problem:     r.URL.Query().Get("problem"),
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
	if v.CanShare {
		v.Bindings = s.shelfBindings(r, entity.ID)
		v.SplitFolders = s.shelfFolders(r, entity.ID)
		v.MergeTargetID = r.URL.Query().Get("merge")
		v.MergeTargetName = r.URL.Query().Get("mergename")
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
// Renaming
// -------------------------------------------------------------------

// handleSeriesRename takes the rename form on the shelf (ADR-0020). It
// redirects rather than swapping a fragment, because the name is in the
// page title, the heading and the breadcrumb: reloading the shelf is
// the honest way to show it changed.
func (s *Server) handleSeriesRename(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	entityID := r.PathValue("entity")
	back := "../" + url.PathEscape(entityID)
	scope := store.SeriesSourcePersonal
	if r.FormValue("scope") == string(store.SeriesSourceShared) {
		if u == nil || !u.IsAdmin {
			redirectRel(w, back+"?problem="+url.QueryEscape(
				"Only an administrator changes what everybody sees."),
				http.StatusSeeOther)
			return
		}
		scope = store.SeriesSourceShared
	}

	var err error
	notice := "Renamed."
	if r.FormValue("reset") != "" {
		notice = "Back to the name in the library."
		err = s.St.ClearSeriesName(r.Context(), readerID(u), entityID, scope)
	} else {
		err = s.St.SetSeriesName(r.Context(), readerID(u), entityID, scope,
			r.FormValue("name"), time.Now().UTC())
	}
	if err != nil {
		problem := back + "?problem=" + url.QueryEscape(renameProblem(err))
		// A name already taken is not a failure so much as a different
		// request: the reader is asking for one shelf where there are
		// two. An admin can have that, so the page is sent back holding
		// the merge rather than a dead end (ADR-0021).
		if errors.Is(err, store.ErrConflict) && u != nil && u.IsAdmin {
			if target, ok := s.seriesByName(r, u, r.FormValue("name")); ok &&
				target.ID != entityID {
				problem += "&merge=" + url.QueryEscape(target.ID) +
					"&mergename=" + url.QueryEscape(target.Name)
			}
		}
		redirectRel(w, problem, http.StatusSeeOther)
		return
	}
	redirectRel(w, back+"?notice="+url.QueryEscape(notice), http.StatusSeeOther)
}

// renameProblem says what went wrong in the reader's terms. A conflict
// is the one worth spelling out: it is not a failure so much as a
// request the server cannot honour, because merging two shelves is not
// something it can do.
func renameProblem(err error) string {
	switch {
	case errors.Is(err, store.ErrConflict):
		return "Another series already has that name."
	case errors.Is(err, store.ErrInvalidInput):
		return "A series needs a name."
	default:
		return "That could not be saved."
	}
}

// -------------------------------------------------------------------
// Merging and splitting (ADR-0021)
// -------------------------------------------------------------------

// shelfBindings lists what this shelf absorbed. A failure is not worth a
// page: the card simply has nothing to offer.
func (s *Server) shelfBindings(r *http.Request, seriesID string) []SeriesBindingRow {
	bindings, err := s.St.SeriesBindings(r.Context(), seriesID)
	if err != nil {
		return nil
	}
	out := make([]SeriesBindingRow, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, SeriesBindingRow{
			ID: b.ID, Name: b.Name, Folder: b.FolderName,
		})
	}
	return out
}

func (s *Server) shelfFolders(r *http.Request, seriesID string) []SeriesFolderRow {
	folders, err := s.St.SeriesFolders(r.Context(), seriesID)
	if err != nil {
		return nil
	}
	out := make([]SeriesFolderRow, 0, len(folders))
	for _, f := range folders {
		out = append(out, SeriesFolderRow{
			ID: f.FolderID, Name: f.Name, Books: f.BookCount,
		})
	}
	return out
}

// seriesByName finds the shelf a typed name belongs to, folding case and
// spacing the way the catalog does. It matches the name a reader sees
// and the name the last pass observed, because either is what they might
// have typed.
func (s *Server) seriesByName(
	r *http.Request, u *store.User, name string,
) (store.CatalogEntity, bool) {
	needle := metadata.NormalizeName(name)
	if needle == "" {
		return store.CatalogEntity{}, false
	}
	rows, err := s.St.ListCatalogEntities(
		r.Context(), readerID(u), store.EntitySeries, "", store.MaxEntityListLimit)
	if err != nil {
		return store.CatalogEntity{}, false
	}
	for _, row := range rows {
		if metadata.NormalizeName(row.Name) == needle ||
			metadata.NormalizeName(row.ScannedName) == needle {
			return row, true
		}
	}
	return store.CatalogEntity{}, false
}

// requireCurator answers the request itself unless an admin sent it.
// Merging and splitting speak for the whole library, so unlike a rename
// they have no personal layer to fall back to.
func (s *Server) requireCurator(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, back string,
) bool {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if u == nil || !u.IsAdmin {
		redirectRel(w, back+"?problem="+url.QueryEscape(
			"Only an administrator reshapes the library's shelves."),
			http.StatusSeeOther)
		return false
	}
	return true
}

// handleSeriesMerge folds this shelf into another. The absorbed shelf's
// page stops existing, so the redirect goes to the survivor.
func (s *Server) handleSeriesMerge(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	entityID := r.PathValue("entity")
	back := "../" + url.PathEscape(entityID)
	if !s.requireCurator(w, r, a, u, back) {
		return
	}
	intoID := strings.TrimSpace(r.FormValue("into"))
	if intoID == "" {
		target, ok := s.seriesByName(r, u, r.FormValue("into_name"))
		if !ok {
			redirectRel(w, back+"?problem="+url.QueryEscape(
				"No shelf in the library goes by that name."), http.StatusSeeOther)
			return
		}
		intoID = target.ID
	}
	survivor, err := s.St.MergeSeries(
		r.Context(), readerID(u), entityID, intoID, time.Now().UTC())
	if err != nil {
		redirectRel(w, back+"?problem="+url.QueryEscape(mergeProblem(err)),
			http.StatusSeeOther)
		return
	}
	redirectRel(w, "../"+url.PathEscape(survivor)+"?notice="+url.QueryEscape(
		"Merged. The old name now leads here, so a scan will not undo it."),
		http.StatusSeeOther)
}

// handleSeriesSplit takes one folder's books off the shelf. The redirect
// goes to the new shelf, because that is what the curator just made.
func (s *Server) handleSeriesSplit(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	entityID := r.PathValue("entity")
	back := "../" + url.PathEscape(entityID)
	if !s.requireCurator(w, r, a, u, back) {
		return
	}
	newID, err := s.St.SplitSeriesFolder(
		r.Context(), readerID(u), entityID,
		r.FormValue("folder_id"), r.FormValue("name"), time.Now().UTC())
	if err != nil {
		redirectRel(w, back+"?problem="+url.QueryEscape(splitProblem(err)),
			http.StatusSeeOther)
		return
	}
	redirectRel(w, "../"+url.PathEscape(newID)+"?notice="+url.QueryEscape(
		"Split. That folder keeps this shelf to itself from now on."),
		http.StatusSeeOther)
}

// handleSeriesUnbind undoes a merge or a split. No book moves here: the
// next pass over a folder that observes the freed name mints the shelf
// again and refills it from what the folder says.
func (s *Server) handleSeriesUnbind(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	back := "../" + url.PathEscape(r.PathValue("entity"))
	if !s.requireCurator(w, r, a, u, back) {
		return
	}
	if err := s.St.DeleteSeriesBinding(
		r.Context(), r.FormValue("binding"),
	); err != nil {
		redirectRel(w, back+"?problem="+url.QueryEscape("That could not be undone."),
			http.StatusSeeOther)
		return
	}
	redirectRel(w, back+"?notice="+url.QueryEscape(
		"Undone. The next scan puts those books back where the folder says."),
		http.StatusSeeOther)
}

func mergeProblem(err error) string {
	switch {
	case errors.Is(err, store.ErrInvalidInput):
		return "A shelf cannot be merged into itself."
	case errors.Is(err, store.ErrNotFound):
		return "That shelf is not in the library any more."
	default:
		return "That could not be merged."
	}
}

func splitProblem(err error) string {
	switch {
	case errors.Is(err, store.ErrConflict):
		return "Another shelf already has that name."
	case errors.Is(err, store.ErrNotFound):
		return "That folder has no books on this shelf."
	case errors.Is(err, store.ErrInvalidInput):
		return "Every book here came from that folder, so that is a rename."
	default:
		return "That could not be split."
	}
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

// folderBooksLabel names a folder and how much of the shelf came from
// it, so a split says what would move before it moves.
func folderBooksLabel(f SeriesFolderRow) string {
	if f.Books == 1 {
		return f.Name + " (1 book)"
	}
	return f.Name + " (" + strconv.Itoa(f.Books) + " books)"
}

// bindingLabel says what a binding covers. A merge binds everywhere and
// a split binds in one folder, and which it is decides what undoing it
// would do.
func bindingLabel(b SeriesBindingRow) string {
	if b.Folder == "" {
		return b.Name + " — everywhere"
	}
	return b.Name + " — in " + b.Folder
}
