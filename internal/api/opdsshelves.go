package api

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// finishedProgression is where a book counts as read to the end. It is
// the web library's threshold, so the two never disagree about a book.
const finishedProgression = 0.999

// opdsShelf is one personal view of the library, offered from the root
// feed. Unlike the folder feeds it depends on the reader's own position
// in each book.
type opdsShelf struct {
	slug, title string
	// keep selects books by reading state; nil keeps every book.
	keep func(shelfState) bool
	// less orders the kept books; nil keeps the catalog's newest-first order.
	less func(a, b shelfBook) bool
}

type shelfState struct {
	progression *float64
	lastActive  time.Time
}

func (st shelfState) started() bool {
	return st.progression != nil && *st.progression > 0
}

type shelfBook struct {
	book  store.CatalogBook
	state shelfState
}

func byLastRead(a, b shelfBook) bool {
	if !a.state.lastActive.Equal(b.state.lastActive) {
		return a.state.lastActive.After(b.state.lastActive)
	}
	return a.book.ID < b.book.ID
}

var opdsShelves = []opdsShelf{
	{
		slug: "reading", title: "Currently reading",
		keep: func(st shelfState) bool {
			return st.started() && *st.progression < finishedProgression
		},
		less: byLastRead,
	},
	{
		slug: "unread", title: "Unread",
		keep: func(st shelfState) bool { return !st.started() },
	},
	{
		slug: "last-read", title: "By last read",
		keep: shelfState.started,
		less: byLastRead,
	},
	{
		slug: "alphabetical", title: "A to Z",
		less: func(a, b shelfBook) bool {
			ta, tb := strings.ToLower(a.book.Title), strings.ToLower(b.book.Title)
			if ta != tb {
				return ta < tb
			}
			return a.book.ID < b.book.ID
		},
	},
}

func opdsShelfBySlug(slug string) (opdsShelf, bool) {
	for _, sh := range opdsShelves {
		if sh.slug == slug {
			return sh, true
		}
	}
	return opdsShelf{}, false
}

// opdsShelfFacets are the root feed's header links to the shelves.
func opdsShelfFacets() []opdsLink {
	out := make([]opdsLink, 0, len(opdsShelves))
	for _, sh := range opdsShelves {
		out = append(out, opdsLink{
			Rel:  opdsFacetRel,
			Href: opdsPrefix + "/shelves/" + sh.slug,
			Type: opdsAcquisitionType, Title: sh.title,
		})
	}
	return out
}

// readerBookStates maps each catalog book to this reader's newest
// position in it. A book with no entry has never been opened.
func (s *Server) readerBookStates(r *http.Request, userID string) (map[string]shelfState, error) {
	works, err := s.St.ListWorks(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]shelfState)
	for _, ws := range works {
		if ws.Progression == nil {
			continue
		}
		ids, err := s.St.WorkBookIDs(r.Context(), userID, ws.Work.ID)
		if err != nil {
			return nil, err
		}
		st := shelfState{progression: ws.Progression}
		if ws.LastActive != nil {
			st.lastActive = *ws.LastActive
		}
		for _, id := range ids {
			if old, ok := out[id]; !ok || st.lastActive.After(old.lastActive) {
				out[id] = st
			}
		}
	}
	return out, nil
}

// allLibraryBooks walks the viewer's whole catalog, newest first. The
// shelves sort by something the catalog cursor cannot cut on, so they
// order the full set and page it themselves.
func (s *Server) allLibraryBooks(r *http.Request, userID string) ([]store.CatalogBook, error) {
	var all []store.CatalogBook
	var cursor *store.CatalogBookCursor
	for {
		page, err := s.St.ListRecentLibraryBooks(r.Context(), userID, cursor, maxCatalogPageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < maxCatalogPageSize {
			return all, nil
		}
		last := page[len(page)-1]
		cursor = &store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
}

// HandleOPDSShelf serves one personal shelf as an acquisition feed.
func (s *Server) HandleOPDSShelf(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	shelf, page, next, ok := s.opdsShelfPage(w, r, tok.UserID)
	if !ok {
		return
	}
	self := opdsPrefix + "/shelves/" + shelf.slug
	feed := opdsFeed{
		ID:      "urn:liseur:shelf:" + shelf.slug,
		Title:   shelf.title,
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsAcquisitionType},
			{Rel: "start", Href: opdsPrefix, Type: opdsAcquisitionType},
			{Rel: "up", Href: opdsPrefix, Type: opdsAcquisitionType},
		},
	}
	entries, err := s.opdsBookEntries(r.Context(), tok.UserID, page)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	feed.Entries = entries
	if next != "" {
		feed.Links = append(feed.Links, opdsLink{
			Rel:  "next",
			Type: opdsAcquisitionType,
			Href: self + "?offset=" + url.QueryEscape(next),
		})
	}
	writeOPDS(w, r, opdsAcquisitionType, feed)
}

// opdsShelfPage reads the shelf named in the path and the page of it the
// `offset` parameter asks for, with the offset of the next page when
// there is one. On failure it has already answered.
func (s *Server) opdsShelfPage(
	w http.ResponseWriter, r *http.Request, userID string,
) (opdsShelf, []store.CatalogBook, string, bool) {
	shelf, ok := opdsShelfBySlug(r.PathValue("shelf"))
	if !ok {
		writeError(w, http.StatusNotFound, "shelf not found")
		return opdsShelf{}, nil, "", false
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return opdsShelf{}, nil, "", false
		}
		offset = n
	}
	books, err := s.allLibraryBooks(r, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return opdsShelf{}, nil, "", false
	}
	states, err := s.readerBookStates(r, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading state lookup failed")
		return opdsShelf{}, nil, "", false
	}
	kept := make([]shelfBook, 0, len(books))
	for _, b := range books {
		st := states[b.ID]
		if shelf.keep == nil || shelf.keep(st) {
			kept = append(kept, shelfBook{book: b, state: st})
		}
	}
	if shelf.less != nil {
		sort.SliceStable(kept, func(i, j int) bool { return shelf.less(kept[i], kept[j]) })
	}

	end := min(offset+opdsPageSize, len(kept))
	page := []store.CatalogBook{}
	if offset < len(kept) {
		for _, k := range kept[offset:end] {
			page = append(page, k.book)
		}
	}
	next := ""
	if end < len(kept) {
		next = strconv.Itoa(end)
	}
	return shelf, page, next, true
}
