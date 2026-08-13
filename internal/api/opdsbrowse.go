package api

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// opdsSearchType is the media type of the description document. A reader
// finds it through a `search` link and reads the template out of it, so
// the search URL is never something a reader has to be told.
const opdsSearchType = "application/opensearchdescription+xml"

// opdsSearchDescription is the OpenSearch document. It is a struct for
// the same reason every feed is: the library name it carries came out of
// a database a person types into.
type opdsSearchDescription struct {
	XMLName     xml.Name        `xml:"OpenSearchDescription"`
	Xmlns       string          `xml:"xmlns,attr"`
	ShortName   string          `xml:"ShortName"`
	Description string          `xml:"Description"`
	InputEnc    string          `xml:"InputEncoding"`
	URLs        []opdsSearchURL `xml:"Url"`
}

type opdsSearchURL struct {
	Type     string `xml:"type,attr"`
	Template string `xml:"template,attr"`
}

// HandleOPDSSearchDescription serves the OpenSearch document for one
// library at /opds/v1.2/libraries/{library}/search.xml.
//
// The library is checked before the document is written, so a document
// never confirms a library the caller may not read.
func (s *Server) HandleOPDSSearchDescription(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID := r.PathValue("library")
	if libraryID == "" || len(libraryID) > maxLibraryIDBytes {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}
	lib, err := s.St.LibraryByID(r.Context(), tok.UserID, libraryID, store.LibraryRoleRead)
	if err != nil {
		writeCatalogError(w, err, "library not found")
		return
	}
	base := opdsPrefix + "/libraries/" + url.PathEscape(libraryID) + "/search"
	doc := opdsSearchDescription{
		Xmlns:       "http://a9.com/-/spec/opensearch/1.1/",
		ShortName:   opdsTitle(lib.Library.Name, "Library"),
		Description: "Search this library",
		InputEnc:    "UTF-8",
		URLs: []opdsSearchURL{{
			Type: opdsAcquisitionType,
			// `searchTerms` is the only term offered. A reader that could
			// ask about anything else would be asking a question this
			// catalog deliberately cannot answer.
			Template: base + "?q={searchTerms}",
		}},
	}
	w.Header().Set("Content-Type", opdsSearchType+"; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		return
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(doc)
	_ = enc.Close()
}

// HandleOPDSSearch serves search results as an acquisition feed.
//
// It is unpaged, like the native route it calls, so it carries no `next`
// link: a reader that reaches the end of this feed has seen the best
// matches, and the answer to wanting more is a better query.
func (s *Server) HandleOPDSSearch(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID := r.PathValue("library")
	if libraryID == "" || len(libraryID) > maxLibraryIDBytes {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}
	text := r.URL.Query().Get("q")
	if len(text) > maxSearchTextBytes {
		writeError(w, http.StatusBadRequest, "search text is too long")
		return
	}
	result, err := s.St.SearchCatalogBooks(r.Context(), tok.UserID, store.SearchQuery{
		LibraryID: libraryID,
		Text:      text,
		Limit:     defaultCatalogPageSize,
	})
	if err != nil {
		writeCatalogError(w, err, "library not found")
		return
	}
	self := opdsPrefix + "/libraries/" + url.PathEscape(libraryID) +
		"/search?q=" + url.QueryEscape(text)
	feed := opdsFeed{
		ID:      "urn:liseur:library:" + libraryID + ":search",
		Title:   "Search results",
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsAcquisitionType},
			{Rel: "start", Href: opdsPrefix, Type: opdsNavigationType},
			{Rel: "up", Href: opdsPrefix + "/libraries/" + url.PathEscape(libraryID),
				Type: opdsAcquisitionType},
		},
	}
	for _, b := range result.Books {
		feed.Entries = append(feed.Entries, opdsBookEntry(b))
	}
	writeOPDS(w, r, opdsAcquisitionType, feed)
}

// opdsKindTitles name the browse feeds. The segments are the same ones
// the native API uses, so one URL vocabulary covers both surfaces.
var opdsKindTitles = map[string]string{
	"series":       "Series",
	"contributors": "Contributors",
	"tags":         "Tags",
	"genres":       "Genres",
}

// HandleOPDSEntities serves one library's series, contributors, tags or
// genres as a navigation feed: each entry leads to the books claiming it.
func (s *Server) HandleOPDSEntities(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID, kind, ok := entityRequest(w, r)
	if !ok {
		return
	}
	segment := r.PathValue("kind")
	after := r.URL.Query().Get("after")
	if len(after) > 512 {
		writeError(w, http.StatusBadRequest, "cursor is too long")
		return
	}
	limit := defaultCatalogPageSize
	entities, err := s.St.ListCatalogEntities(
		r.Context(), tok.UserID, libraryID, kind, after, limit)
	if err != nil {
		writeCatalogError(w, err, "library not found")
		return
	}
	libraryHref := opdsPrefix + "/libraries/" + url.PathEscape(libraryID)
	self := libraryHref + "/" + segment
	feed := opdsFeed{
		ID:      "urn:liseur:library:" + libraryID + ":" + segment,
		Title:   opdsKindTitles[segment],
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsNavigationType},
			{Rel: "start", Href: opdsPrefix, Type: opdsNavigationType},
			{Rel: "up", Href: libraryHref, Type: opdsAcquisitionType},
		},
	}
	for _, e := range entities {
		feed.Entries = append(feed.Entries, opdsEntry{
			Title:   opdsTitle(e.Name, "Untitled"),
			ID:      "urn:liseur:entity:" + e.ID,
			Updated: opdsTime(time.Now()),
			Content: &opdsText{Type: "text", Body: plainPlural(e.BookCount, "book")},
			Links: []opdsLink{{
				Rel:  "subsection",
				Href: self + "/" + url.PathEscape(e.ID),
				Type: opdsAcquisitionType,
			}},
		})
	}
	if len(entities) == limit {
		feed.Links = append(feed.Links, opdsLink{
			Rel: "next", Type: opdsNavigationType,
			Href: self + "?after=" +
				url.QueryEscape(entities[len(entities)-1].NormalizedName),
		})
	}
	writeOPDS(w, r, opdsNavigationType, feed)
}

// HandleOPDSEntityBooks serves the books claiming one entity. A series
// comes back in reading order, which is the whole reason a reader would
// rather browse a series than search for it.
func (s *Server) HandleOPDSEntityBooks(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID, kind, ok := entityRequest(w, r)
	if !ok {
		return
	}
	segment := r.PathValue("kind")
	after, err := decodeCatalogCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entity, err := s.St.CatalogEntityByID(
		r.Context(), tok.UserID, libraryID, r.PathValue("entity"), kind)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return
	}
	limit := defaultCatalogPageSize
	books, err := s.St.ListBooksByEntity(
		r.Context(), tok.UserID, libraryID, entity.ID, kind, after, limit)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return
	}
	kindHref := opdsPrefix + "/libraries/" + url.PathEscape(libraryID) + "/" + segment
	self := kindHref + "/" + url.PathEscape(entity.ID)
	feed := opdsFeed{
		ID:      "urn:liseur:entity:" + entity.ID,
		Title:   opdsTitle(entity.Name, "Untitled"),
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsAcquisitionType},
			{Rel: "start", Href: opdsPrefix, Type: opdsNavigationType},
			{Rel: "up", Href: kindHref, Type: opdsNavigationType},
		},
	}
	for _, b := range books {
		feed.Entries = append(feed.Entries, opdsBookEntry(b))
	}
	if len(books) == limit {
		last := books[len(books)-1]
		feed.Links = append(feed.Links, opdsLink{
			Rel: "next", Type: opdsAcquisitionType,
			Href: self + "?cursor=" + url.QueryEscape(encodeCatalogCursor(
				store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID})),
		})
	}
	writeOPDS(w, r, opdsAcquisitionType, feed)
}

// plainPlural counts without the web UI's markup, since a feed's content
// is text a reader renders however it likes.
func plainPlural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// HandleOPDSRecent serves the library's newest books, which is the feed a
// reader opens to see what has arrived since last time. It is reached
// from the library's own feed rather than the root, because "recent" only
// means anything inside one library.
func (s *Server) HandleOPDSRecent(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libraryID := r.PathValue("library")
	if libraryID == "" || len(libraryID) > maxLibraryIDBytes {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}
	before, err := decodeCatalogCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := defaultCatalogPageSize
	books, err := s.St.ListRecentCatalogBooks(r.Context(), tok.UserID, libraryID, before, limit)
	if err != nil {
		writeCatalogError(w, err, "library not found")
		return
	}
	libraryHref := opdsPrefix + "/libraries/" + url.PathEscape(libraryID)
	self := libraryHref + "/recent"
	feed := opdsFeed{
		ID:      "urn:liseur:library:" + libraryID + ":recent",
		Title:   "Recently added",
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsAcquisitionType},
			{Rel: "start", Href: opdsPrefix, Type: opdsNavigationType},
			{Rel: "up", Href: libraryHref, Type: opdsAcquisitionType},
		},
	}
	for _, b := range books {
		feed.Entries = append(feed.Entries, opdsBookEntry(b))
	}
	if len(books) == limit {
		last := books[len(books)-1]
		feed.Links = append(feed.Links, opdsLink{
			Rel: "next", Type: opdsAcquisitionType,
			Href: self + "?cursor=" + url.QueryEscape(encodeCatalogCursor(
				store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID})),
		})
	}
	writeOPDS(w, r, opdsAcquisitionType, feed)
}
