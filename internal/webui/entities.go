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

// handleEntities lists the library's entities of one kind (ADR-0019),
// with counts and rows limited to books visible through folder grants.
// readerID is who a catalog read is answered for. Series memberships
// resolve through that reader's override layers (ADR-0018), while folder
// grants decide which backing books are visible (ADR-0027).
func readerID(u *store.User) string {
	if u == nil {
		return ""
	}
	return u.ID
}

func (s *Server) handleEntities(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	kind, ok := entityKindFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rows, err := s.St.ListCatalogEntities(
		r.Context(), readerID(u), kind,
		r.URL.Query().Get("after"), entityPageSize)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	labels := entityKindLabels[kind]
	v := EntitiesView{
		Kind:     r.PathValue("kind"),
		Heading:  labels.Many,
		Singular: labels.One,
		Notice:   r.URL.Query().Get("notice"),
		Problem:  r.URL.Query().Get("problem"),
		View:     readPrefs(r).View,
	}
	// The view toggle returns here. The preference form posts to
	// /ui/preferences and its redirect is resolved from the UI root, so
	// this is the path relative to /ui/ — the whole route, not the kind
	// segment the browser happens to be standing on.
	v.Back = uiEntityList(v.Kind)
	for _, row := range rows {
		// A series nothing claims any more is a name and not a shelf.
		// This became reachable rather than theoretical with ADR-0018:
		// a claim can move every book out of a series, and nothing
		// collects the series row that is left behind. Contributors and
		// tags keep their empty rows, which say "the folder knows this
		// name and has nothing to show for it" — a series says the same
		// thing by not being there.
		if kind == store.EntitySeries && row.BookCount == 0 {
			continue
		}
		v.Entities = append(v.Entities, EntityRow{
			ID: row.ID, Name: row.Name, BookCount: row.BookCount,
		})
	}
	if len(rows) == entityPageSize {
		v.NextURL = uiEntityList(v.Kind) + "?after=" +
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
	entityID := r.PathValue("entity")
	entity, err := s.St.CatalogEntityByID(
		r.Context(), readerID(u), entityID, kind)
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
		r.Context(), readerID(u), entity.ID, kind, cursor, entityPageSize)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	bookIDs := make([]string, 0, len(books))
	for _, b := range books {
		bookIDs = append(bookIDs, b.ID)
	}
	authors, _ := s.St.CatalogAuthorsForBooks(r.Context(), u.ID, bookIDs)
	loc := userLoc(u)
	v := EntityBooksView{
		Kind:     r.PathValue("kind"),
		Heading:  entity.Name,
		Singular: entityKindLabels[kind].One,
		View:     readPrefs(r).View,
		Back:     uiEntity(r.PathValue("kind"), entityID),
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
		v.NextURL = uiEntity(v.Kind, entityID) +
			"?cursor=" + url.QueryEscape(encodeBooksCursor(*next))
	}
	entityBooksPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}
