package webui

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// entityKinds is the closed set of things a library groups its books by.
// It is a fixed map rather than a cast because the kind selects a table:
// a path segment must never reach the store as one.
var entityKinds = map[string]store.EntityKind{
	"series":       store.EntitySeries,
	"contributors": store.EntityContributor,
	"tags":         store.EntityTag,
	"genres":       store.EntityGenre,
}

// entityKindLabels name each kind in the singular and plural a page
// needs. They are here rather than in the template because the merge
// form and the flash messages want the same words.
var entityKindLabels = map[store.EntityKind]struct{ One, Many string }{
	store.EntitySeries:      {"series", "Series"},
	store.EntityContributor: {"contributor", "Contributors"},
	store.EntityTag:         {"tag", "Tags"},
	store.EntityGenre:       {"genre", "Genres"},
}

const entityPageSize = 100

// entityKindFrom resolves the path segment. An unknown kind is a 404
// rather than a 400: it names no resource.
func entityKindFrom(r *http.Request) (store.EntityKind, bool) {
	kind, ok := entityKinds[r.PathValue("kind")]
	return kind, ok
}

// handleEntities lists one library's entities of one kind. Read access
// is enough, because browsing by author or tag is reading.
func (s *Server) handleEntities(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	kind, ok := entityKindFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	libraryID := r.PathValue("library")
	rows, err := s.St.ListCatalogEntities(
		r.Context(), u.ID, libraryID, kind, r.URL.Query().Get("after"), entityPageSize)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	labels := entityKindLabels[kind]
	v := EntitiesView{
		LibraryID: libraryID,
		Kind:      r.PathValue("kind"),
		Heading:   labels.Many,
		Singular:  labels.One,
		Notice:    r.URL.Query().Get("notice"),
		Problem:   r.URL.Query().Get("problem"),
		View:      readPrefs(r).View,
	}
	// The view toggle returns here. A relative target is resolved by the
	// browser against this URL, so the kind segment alone is this page.
	v.Back = url.PathEscape(v.Kind)
	// Only a librarian is offered rename and merge, for the same reason
	// they are the only one offered the trash: these are decisions about
	// the library rather than facts about a book.
	if _, err := s.St.LibraryByID(
		r.Context(), u.ID, libraryID, store.LibraryRoleManage); err == nil {
		v.CanWrite = true
	}
	for _, row := range rows {
		v.Entities = append(v.Entities, EntityRow{
			ID: row.ID, Name: row.Name, BookCount: row.BookCount,
		})
	}
	if len(rows) == entityPageSize {
		v.NextURL = "libraries/" + url.PathEscape(libraryID) + "/" +
			url.PathEscape(v.Kind) + "?after=" +
			url.QueryEscape(rows[len(rows)-1].NormalizedName)
	}
	entitiesPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}

// handleEntityBooks lists the books claiming one entity.
func (s *Server) handleEntityBooks(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	kind, ok := entityKindFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	libraryID, entityID := r.PathValue("library"), r.PathValue("entity")
	entity, err := s.St.CatalogEntityByID(r.Context(), u.ID, libraryID, entityID, kind)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cursor, err := decodeBooksCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	books, err := s.St.ListBooksByEntity(
		r.Context(), u.ID, libraryID, entityID, kind, cursor, entityPageSize)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc := userLoc(u)
	v := EntityBooksView{
		LibraryID: libraryID,
		Kind:      r.PathValue("kind"),
		Heading:   entity.Name,
		Singular:  entityKindLabels[kind].One,
		View:      readPrefs(r).View,
		Back:      url.PathEscape(entityID),
	}
	for _, b := range books {
		v.Books = append(v.Books, BookRow{
			ID: b.ID, Title: b.Title,
			Added: b.CreatedAt.In(loc).Format("Jan 2, 2006"),
		})
	}
	if len(books) == entityPageSize {
		last := books[len(books)-1]
		v.NextURL = "libraries/" + url.PathEscape(libraryID) + "/" +
			url.PathEscape(v.Kind) + "/" + url.PathEscape(entityID) +
			"?cursor=" + url.QueryEscape(encodeBooksCursor(
			store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID}))
	}
	entityBooksPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}

// handleRenameEntity changes one entity's display spelling. A name
// another entity already holds is refused with an offer to merge, rather
// than quietly becoming a merge: folding two things into one is a
// different decision and deserves to be made on purpose.
func (s *Server) handleRenameEntity(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	kind, ok := entityKindFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	libraryID := r.PathValue("library")
	if !s.checkCSRF(r, a) {
		s.entityResult(w, r, libraryID, "", "that form had expired; try again")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.entityResult(w, r, libraryID, "", "a name cannot be blank")
		return
	}
	_, err := s.St.RenameCatalogEntity(
		r.Context(), u.ID, libraryID, r.PathValue("entity"), kind, name)
	switch {
	case errors.Is(err, store.ErrConflict):
		s.entityResult(w, r, libraryID, "",
			"another "+entityKindLabels[kind].One+" is already called “"+name+
				"”; merge the two instead")
	case err != nil:
		s.entityResult(w, r, libraryID, "",
			"that could not be renamed, or you cannot manage this library")
	default:
		s.entityResult(w, r, libraryID, "renamed", "")
	}
}

// handleMergeEntities folds one entity into another. It is deliberately
// not reversible and says so on the form, because undoing it would mean
// remembering which books had claimed which spelling — state nobody
// asked the server to keep.
func (s *Server) handleMergeEntities(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	kind, ok := entityKindFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	libraryID := r.PathValue("library")
	if !s.checkCSRF(r, a) {
		s.entityResult(w, r, libraryID, "", "that form had expired; try again")
		return
	}
	from, into := r.FormValue("from"), r.FormValue("into")
	if from == "" || into == "" {
		s.entityResult(w, r, libraryID, "", "pick both sides of the merge")
		return
	}
	if from == into {
		s.entityResult(w, r, libraryID, "", "those are the same one")
		return
	}
	moved, err := s.St.MergeCatalogEntities(
		r.Context(), u.ID, libraryID, from, into, kind, time.Now().UTC())
	if err != nil {
		s.entityResult(w, r, libraryID, "",
			"that merge could not be applied, or you cannot manage this library")
		return
	}
	s.entityResult(w, r, libraryID, mergeNotice(moved), "")
}

// mergeNotice reports the merge in books rather than rows, because rows
// are the store's word for it and a librarian is looking at books.
func mergeNotice(moved int) string {
	switch moved {
	case 0:
		return "merged; every book already had both"
	case 1:
		return "merged; 1 book moved"
	default:
		return "merged; " + strconv.Itoa(moved) + " books moved"
	}
}

func (s *Server) entityResult(
	w http.ResponseWriter, r *http.Request, libraryID, notice, problem string,
) {
	q := url.Values{}
	if notice != "" {
		q.Set("notice", notice)
	}
	if problem != "" {
		q.Set("problem", problem)
	}
	target := relPrefix(r.URL.Path) + "libraries/" + url.PathEscape(libraryID) +
		"/" + url.PathEscape(r.PathValue("kind"))
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	redirectRel(w, target, http.StatusSeeOther)
}
