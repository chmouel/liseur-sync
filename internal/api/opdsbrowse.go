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
// the same reason every feed is: the folder name it carries came out of
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
// folder at /opds/v1.2/folders/{folder}/search.xml.
//
// The folder is checked before the document is written, so a document
// never confirms a folder that does not exist.
func (s *Server) HandleOPDSSearchDescription(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	folderID := r.PathValue("folder")
	if folderID == "" || len(folderID) > maxFolderIDBytes {
		writeError(w, http.StatusNotFound, "folder not found")
		return
	}
	folder, err := s.St.FolderByID(r.Context(), tok.UserID, folderID)
	if err != nil {
		writeCatalogError(w, err, "folder not found")
		return
	}
	base := opdsPrefix + "/folders/" + url.PathEscape(folderID) + "/search"
	doc := opdsSearchDescription{
		Xmlns:       "http://a9.com/-/spec/opensearch/1.1/",
		ShortName:   opdsTitle(folder.Name, "Folder"),
		Description: "Search this folder",
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
	folderID, text, books, ok := s.opdsSearchResults(w, r, tok.UserID, "q")
	if !ok {
		return
	}
	self := opdsPrefix + "/folders/" + url.PathEscape(folderID) +
		"/search?q=" + url.QueryEscape(text)
	feed := opdsFeed{
		ID:      "urn:liseur:folder:" + folderID + ":search",
		Title:   "Search results",
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsAcquisitionType},
			{Rel: "start", Href: opdsPrefix, Type: opdsAcquisitionType},
			{Rel: "up", Href: opdsPrefix + "/folders/" + url.PathEscape(folderID),
				Type: opdsAcquisitionType},
		},
	}
	entries, err := s.opdsBookEntries(r.Context(), tok.UserID, books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog search failed")
		return
	}
	feed.Entries = entries
	writeOPDS(w, r, opdsAcquisitionType, feed)
}

// opdsSearchResults runs the search a feed asked for. The query parameter
// is named by the caller because the two OPDS versions spell it
// differently: OpenSearch templates carry `q`, an OPDS 2.0 URI template
// `query`. On failure it has already answered.
func (s *Server) opdsSearchResults(
	w http.ResponseWriter, r *http.Request, userID, param string,
) (folderID, text string, books []store.CatalogBook, ok bool) {
	folderID = r.PathValue("folder")
	if folderID == "" || len(folderID) > maxFolderIDBytes {
		writeError(w, http.StatusNotFound, "folder not found")
		return "", "", nil, false
	}
	text = r.URL.Query().Get(param)
	if len(text) > maxSearchTextBytes {
		writeError(w, http.StatusBadRequest, "search text is too long")
		return "", "", nil, false
	}
	result, err := s.St.SearchCatalogBooks(r.Context(), store.SearchQuery{
		FolderID: folderID,
		UserID:   userID,
		Text:     text,
		Limit:    opdsPageSize,
	})
	if err != nil {
		writeCatalogError(w, err, "folder not found")
		return "", "", nil, false
	}
	return folderID, text, result.Books, true
}

// opdsKindTitles name the browse feeds. The segments are the same ones
// the native API uses, so one URL vocabulary covers both surfaces.
var opdsKindTitles = map[string]string{
	"series":       "Series",
	"contributors": "Contributors",
	"tags":         "Tags",
}

// opdsEntitySegments is the order the browse links are offered in.
var opdsEntitySegments = []string{"series", "contributors", "tags"}

// HandleOPDSEntities serves the library's series, contributors or tags
// as a navigation feed: each entry leads to the books claiming it.
// Entities are library-wide (ADR-0019), so the feed hangs off the root
// rather than off a folder.
func (s *Server) HandleOPDSEntities(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	segment, entities, nextAfter, ok := s.opdsEntitiesPage(w, r)
	if !ok {
		return
	}
	self := opdsPrefix + "/entities/" + segment
	feed := opdsFeed{
		ID:      "urn:liseur:entities:" + segment,
		Title:   opdsKindTitles[segment],
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsNavigationType},
			{Rel: "start", Href: opdsPrefix, Type: opdsAcquisitionType},
			{Rel: "up", Href: opdsPrefix, Type: opdsAcquisitionType},
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
	if nextAfter != "" {
		feed.Links = append(feed.Links, opdsLink{
			Rel: "next", Type: opdsNavigationType,
			Href: self + "?after=" + url.QueryEscape(nextAfter),
		})
	}
	writeOPDS(w, r, opdsNavigationType, feed)
}

// opdsEntitiesPage reads one page of the kind named in the path, and the
// key the next page starts after. On failure it has already answered.
func (s *Server) opdsEntitiesPage(
	w http.ResponseWriter, r *http.Request,
) (segment string, entities []store.CatalogEntity, nextAfter string, ok bool) {
	kind, ok := entityRequest(w, r)
	if !ok {
		return "", nil, "", false
	}
	after := r.URL.Query().Get("after")
	if len(after) > 512 {
		writeError(w, http.StatusBadRequest, "cursor is too long")
		return "", nil, "", false
	}
	entities, err := s.St.ListCatalogEntities(
		r.Context(), readerID(r), kind, after, opdsPageSize)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return "", nil, "", false
	}
	if len(entities) == opdsPageSize {
		nextAfter = entities[len(entities)-1].NormalizedName
	}
	return r.PathValue("kind"), entities, nextAfter, true
}

// HandleOPDSEntityBooks serves the books claiming one entity. A series
// comes back in reading order, which is the whole reason a reader would
// rather browse a series than search for it.
func (s *Server) HandleOPDSEntityBooks(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	segment, entity, books, next, ok := s.opdsEntityBooksPage(w, r)
	if !ok {
		return
	}
	kindHref := opdsPrefix + "/entities/" + segment
	self := kindHref + "/" + url.PathEscape(entity.ID)
	feed := opdsFeed{
		ID:      "urn:liseur:entity:" + entity.ID,
		Title:   opdsTitle(entity.Name, "Untitled"),
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsAcquisitionType},
			{Rel: "start", Href: opdsPrefix, Type: opdsAcquisitionType},
			{Rel: "up", Href: kindHref, Type: opdsNavigationType},
		},
	}
	entries, err := s.opdsBookEntries(r.Context(), readerID(r), books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	feed.Entries = entries
	if next != "" {
		feed.Links = append(feed.Links, opdsLink{
			Rel:  "next",
			Type: opdsAcquisitionType,
			Href: self + "?cursor=" + url.QueryEscape(next),
		})
	}
	writeOPDS(w, r, opdsAcquisitionType, feed)
}

// opdsEntityBooksPage reads the entity named in the path and one page of
// the books claiming it. On failure it has already answered.
func (s *Server) opdsEntityBooksPage(
	w http.ResponseWriter, r *http.Request,
) (segment string, entity store.CatalogEntity, books []store.CatalogBook, next string, ok bool) {
	kind, ok := entityRequest(w, r)
	if !ok {
		return "", store.CatalogEntity{}, nil, "", false
	}
	after, err := decodeCatalogCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", store.CatalogEntity{}, nil, "", false
	}
	entity, err = s.St.CatalogEntityByID(
		r.Context(), readerID(r), r.PathValue("entity"), kind)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return "", store.CatalogEntity{}, nil, "", false
	}
	books, nextCursor, err := s.St.ListBooksByEntity(
		r.Context(), readerID(r), entity.ID, kind, after, opdsPageSize)
	if err != nil {
		writeCatalogError(w, err, "entity not found")
		return "", store.CatalogEntity{}, nil, "", false
	}
	if nextCursor != nil {
		next = encodeCatalogCursor(*nextCursor)
	}
	return r.PathValue("kind"), entity, books, next, true
}

// plainPlural counts without the web UI's markup, since a feed's content
// is text a reader renders however it likes.
func plainPlural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
