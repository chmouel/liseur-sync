package webui

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// entityKindSegments is entityKinds read the other way: a store kind back
// to the URL segment its pages live under, so a facet can link to the
// thing it names.
var entityKindSegments = map[store.EntityKind]string{
	store.EntitySeries:      "series",
	store.EntityContributor: "contributors",
	store.EntityTag:         "tags",
}

// searchPageSize is deliberately below the store's ceiling. A page a
// person reads is shorter than a list a program consumes, and a shorter
// answer makes the facets the way to narrow rather than scrolling.
const searchPageSize = 50

// handleSearch answers a search over one folder. Being signed in is
// enough: the catalog is shared, and finding a book is reading it.
func (s *Server) handleSearch(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	folderID := r.PathValue("folder")
	folder, err := s.St.FolderByID(r.Context(), folderID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	text := strings.TrimSpace(query.Get("q"))
	if len(text) > maxSearchTextBytes {
		text = text[:maxSearchTextBytes]
	}
	filters := query["entity"]
	if len(filters) > maxSearchFilters {
		filters = filters[:maxSearchFilters]
	}
	v := SearchView{
		FolderID:   folderID,
		FolderName: folder.Name,
		Query:      text,
		Filters:    filters,
		View:       readPrefs(r).View,
	}
	// An empty page is what somebody who has only just arrived should
	// see: asking the store for every book would answer a question they
	// have not asked yet.
	if text == "" && len(filters) == 0 {
		v.Blank = true
		searchPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
			Render(r.Context(), w)
		return
	}

	result, err := s.St.SearchCatalogBooks(r.Context(), store.SearchQuery{
		FolderID: folderID,
		Text:     text,
		Entities: filters,
		Limit:    searchPageSize,
	})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v.Truncated = result.Truncated
	bookIDs := make([]string, 0, len(result.Books))
	for _, b := range result.Books {
		bookIDs = append(bookIDs, b.ID)
	}
	authors, _ := s.St.CatalogAuthorsForBooks(r.Context(), bookIDs)

	loc := userLoc(u)
	for _, b := range result.Books {
		v.Books = append(v.Books, BookRow{
			ID: b.ID, Title: b.Title, Author: credit(authors[b.ID]),
			Added:   b.CreatedAt.In(loc).Format("Jan 2, 2006"),
			CanGet:  b.Status == store.BookActive,
			CanRead: bookReadable(b),
		})
	}
	for _, f := range result.Facets {
		// A facet already in force would narrow to what is already
		// shown, so it is offered as a way out rather than a way in.
		if containsString(filters, f.ID) {
			v.Applied = append(v.Applied, SearchFacetRow{
				Kind: entityKindSegments[f.Kind], Name: f.Name,
				URL: v.urlWithout(f.ID),
			})
			continue
		}
		v.Facets = append(v.Facets, SearchFacetRow{
			Kind: entityKindSegments[f.Kind], Name: f.Name,
			BookCount: f.BookCount, URL: v.urlWith(f.ID),
		})
	}
	searchPage(relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), v).
		Render(r.Context(), w)
}

// maxSearchTextBytes and maxSearchFilters mirror the API's bounds. The UI
// trims rather than refusing: a person who pasted a page into the box
// wants a search, not an error.
const (
	maxSearchTextBytes = 512
	maxSearchFilters   = 8
)

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// urlWith and urlWithout build the link that adds or drops one filter,
// keeping everything else the person has already chosen.
func (v SearchView) urlWith(entityID string) string {
	return v.searchURL(append(append([]string{}, v.Filters...), entityID))
}

func (v SearchView) urlWithout(entityID string) string {
	kept := make([]string, 0, len(v.Filters))
	for _, id := range v.Filters {
		if id != entityID {
			kept = append(kept, id)
		}
	}
	return v.searchURL(kept)
}

func (v SearchView) searchURL(filters []string) string {
	values := url.Values{}
	if v.Query != "" {
		values.Set("q", v.Query)
	}
	for _, id := range filters {
		values.Add("entity", id)
	}
	return "folders/" + url.PathEscape(v.FolderID) + "/search?" + values.Encode()
}

// handleTopSearch is what the search box in the top bar submits to.
// Search is a folder-scoped route, but the shell is not: rather than
// give the top bar a folder picker before anybody has asked for one,
// this forwards the query to the first folder this server watches. It
// reads nothing the folder-scoped page could not already read.
func (s *Server) handleTopSearch(
	w http.ResponseWriter, r *http.Request, _ store.AuthSession, u *store.User,
) {
	folders, err := s.St.ListFolders(r.Context(), "", 1)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if len(folders) == 0 {
		// Nothing to search yet. The library page explains how books get
		// here, which is the real answer.
		redirectRel(w, "library", http.StatusSeeOther)
		return
	}
	text := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(text) > maxSearchTextBytes {
		text = text[:maxSearchTextBytes]
	}
	target := "folders/" + url.PathEscape(folders[0].ID) + "/search"
	if text != "" {
		target += "?q=" + url.QueryEscape(text)
	}
	redirectRel(w, target, http.StatusSeeOther)
}
