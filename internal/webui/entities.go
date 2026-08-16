package webui

import (
	"net/http"
	"net/url"

	"github.com/chmouel/liseur-sync/internal/store"
)

// entityKinds is the closed set of things a folder groups its books by.
// It is a fixed map rather than a cast because the kind selects a table:
// a path segment must never reach the store as one.
var entityKinds = map[string]store.EntityKind{
	"series":       store.EntitySeries,
	"contributors": store.EntityContributor,
	"tags":         store.EntityTag,
}

// entityKindLabels name each kind in the singular and plural a page
// needs. They are here rather than in the template because the headings
// and the empty states want the same words.
var entityKindLabels = map[store.EntityKind]struct{ One, Many string }{
	store.EntitySeries:      {"series", "Series"},
	store.EntityContributor: {"contributor", "Contributors"},
	store.EntityTag:         {"tag", "Tags"},
}

const entityPageSize = 100

// entityKindFrom resolves the path segment. An unknown kind is a 404
// rather than a 400: it names no resource.
func entityKindFrom(r *http.Request) (store.EntityKind, bool) {
	kind, ok := entityKinds[r.PathValue("kind")]
	return kind, ok
}

// handleEntities lists one folder's entities of one kind. There is
// nothing to gate: the catalog is shared, and browsing by author or tag
// is reading it (ADR-0017).
func (s *Server) handleEntities(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	kind, ok := entityKindFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	folderID := r.PathValue("folder")
	rows, err := s.St.ListCatalogEntities(
		r.Context(), folderID, kind, r.URL.Query().Get("after"), entityPageSize)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	labels := entityKindLabels[kind]
	v := EntitiesView{
		FolderID: folderID,
		Kind:     r.PathValue("kind"),
		Heading:  labels.Many,
		Singular: labels.One,
		Notice:   r.URL.Query().Get("notice"),
		Problem:  r.URL.Query().Get("problem"),
		View:     readPrefs(r).View,
	}
	// The view toggle returns here. A relative target is resolved by the
	// browser against this URL, so the kind segment alone is this page.
	v.Back = url.PathEscape(v.Kind)
	for _, row := range rows {
		v.Entities = append(v.Entities, EntityRow{
			ID: row.ID, Name: row.Name, BookCount: row.BookCount,
		})
	}
	if len(rows) == entityPageSize {
		v.NextURL = "folders/" + url.PathEscape(folderID) + "/" +
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
	folderID, entityID := r.PathValue("folder"), r.PathValue("entity")
	entity, err := s.St.CatalogEntityByID(r.Context(), folderID, entityID, kind)
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
		r.Context(), folderID, entity.ID, kind, cursor, entityPageSize)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	bookIDs := make([]string, 0, len(books))
	for _, b := range books {
		bookIDs = append(bookIDs, b.ID)
	}
	authors, _ := s.St.CatalogAuthorsForBooks(r.Context(), bookIDs)
	loc := userLoc(u)
	v := EntityBooksView{
		FolderID: folderID,
		Kind:     r.PathValue("kind"),
		Heading:  entity.Name,
		Singular: entityKindLabels[kind].One,
		View:     readPrefs(r).View,
		Back:     url.PathEscape(entityID),
	}
	for _, b := range books {
		v.Books = append(v.Books, BookRow{
			ID: b.ID, Title: b.Title,
			Author:  credit(authors[b.ID]),
			Added:   b.CreatedAt.In(loc).Format("Jan 2, 2006"),
			CanGet:  b.Status == store.BookActive,
			CanRead: bookReadable(b),
		})
	}
	// The store hands back the cursor for the next page, because the
	// sort key depends on the kind and only the store knows it.
	if next != nil {
		v.NextURL = "folders/" + url.PathEscape(folderID) + "/" +
			url.PathEscape(v.Kind) + "/" + url.PathEscape(entityID) +
			"?cursor=" + url.QueryEscape(encodeBooksCursor(*next))
	}
	entityBooksPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}
