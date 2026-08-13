package api

import (
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// OPDS 1.2 lives under a versioned prefix so that a later OPDS 2.0
// surface can be added beside it rather than replacing it.
const opdsPrefix = "/opds/v1.2"

const (
	opdsNavigationType  = "application/atom+xml;profile=opds-catalog;kind=navigation"
	opdsAcquisitionType = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	opdsAcquisitionRel  = "http://opds-spec.org/acquisition"
	atomNS              = "http://www.w3.org/2005/Atom"
	dublinCoreNS        = "http://purl.org/dc/terms/"
	opdsNS              = "http://opds-spec.org/2010/catalog"
)

// opdsFeed is the Atom document. Every feed the server emits is built as
// a struct and handed to encoding/xml: metadata is attacker-supplied
// (it comes out of uploaded EPUBs), and an escaping mistake in a
// hand-built string is a cross-site scripting bug in every reader that
// renders the feed as HTML.
type opdsFeed struct {
	XMLName xml.Name `xml:"feed"`
	Xmlns   string   `xml:"xmlns,attr"`
	XmlnsDC string   `xml:"xmlns:dc,attr"`
	XmlnsOP string   `xml:"xmlns:opds,attr"`

	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Updated string      `xml:"updated"`
	Author  *opdsAuthor `xml:"author,omitempty"`
	Links   []opdsLink  `xml:"link"`
	Entries []opdsEntry `xml:"entry"`
}

type opdsAuthor struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

type opdsLink struct {
	Rel   string `xml:"rel,attr"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr,omitempty"`
}

type opdsEntry struct {
	Title   string    `xml:"title"`
	ID      string    `xml:"id"`
	Updated string    `xml:"updated"`
	Content *opdsText `xml:"content,omitempty"`
	Summary *opdsText `xml:"summary,omitempty"`
	// Dublin Core carries the bibliographic fields Atom has no room for.
	Publisher string     `xml:"dc:publisher,omitempty"`
	Issued    string     `xml:"dc:issued,omitempty"`
	Links     []opdsLink `xml:"link"`
}

type opdsText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

// HandleOPDSRoot serves the navigation feed at /opds/v1.2. It lists the
// libraries the credential can read, which is all a reader needs to
// reach every book: there is no other entry point.
func (s *Server) HandleOPDSRoot(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	libs, err := s.St.ListLibraries(r.Context(), tok.UserID, store.LibraryRoleRead)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "library lookup failed")
		return
	}
	feed := opdsFeed{
		ID:      "urn:liseur:opds:root",
		Title:   "Liseur",
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: opdsPrefix, Type: opdsNavigationType},
			{Rel: "start", Href: opdsPrefix, Type: opdsNavigationType},
		},
	}
	for _, l := range libs {
		href := opdsPrefix + "/libraries/" + url.PathEscape(l.Library.ID)
		feed.Entries = append(feed.Entries, opdsEntry{
			Title:   opdsTitle(l.Library.Name, "Library"),
			ID:      "urn:liseur:library:" + l.Library.ID,
			Updated: opdsTime(l.Library.UpdatedAt),
			Content: &opdsText{Type: "text", Body: "Books in this library"},
			Links: []opdsLink{
				{Rel: "subsection", Href: href, Type: opdsAcquisitionType},
			},
		})
	}
	writeOPDS(w, r, opdsNavigationType, feed)
}

// HandleOPDSLibrary serves the acquisition feed for one library. It is
// paginated with the same cursor as the native catalog, exposed to the
// reader only as an opaque `next` link.
func (s *Server) HandleOPDSLibrary(w http.ResponseWriter, r *http.Request) {
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
	after, err := decodeCatalogCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := defaultCatalogPageSize
	books, err := s.St.ListCatalogBooks(r.Context(), tok.UserID, libraryID, after, limit)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}

	self := opdsPrefix + "/libraries/" + url.PathEscape(libraryID)
	feed := opdsFeed{
		ID:      "urn:liseur:library:" + libraryID,
		Title:   "Library",
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsAcquisitionType},
			{Rel: "start", Href: opdsPrefix, Type: opdsNavigationType},
			{Rel: "up", Href: opdsPrefix, Type: opdsNavigationType},
		},
	}
	for _, b := range books {
		feed.Entries = append(feed.Entries, opdsBookEntry(b))
	}
	if len(books) == limit {
		last := books[len(books)-1]
		cursor := encodeCatalogCursor(store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		feed.Links = append(feed.Links, opdsLink{
			Rel:  "next",
			Href: self + "?cursor=" + url.QueryEscape(cursor),
			Type: opdsAcquisitionType,
		})
	}
	writeOPDS(w, r, opdsAcquisitionType, feed)
}

func opdsBookEntry(b store.CatalogBook) opdsEntry {
	entry := opdsEntry{
		Title:     opdsTitle(b.Title, "Untitled"),
		ID:        "urn:liseur:book:" + b.ID,
		Updated:   opdsTime(b.UpdatedAt),
		Publisher: b.Publisher,
		Issued:    b.PublishedDate,
		Links: []opdsLink{{
			Rel:  opdsAcquisitionRel,
			Href: opdsPrefix + "/books/" + url.PathEscape(b.ID) + "/download",
			Type: "application/epub+zip",
		}},
	}
	// A subtitle is worth showing even when there is no description, so
	// the two are merged into the one field readers reliably render.
	summary := b.Description
	if summary == "" {
		summary = b.Subtitle
	}
	if summary != "" {
		entry.Summary = &opdsText{Type: "text", Body: summary}
	}
	return entry
}

// writeOPDS serialises the feed. It buffers nothing: a feed is small,
// and encoding errors here would mean a struct the encoder cannot
// represent, which is a programming error rather than a runtime one.
func writeOPDS(w http.ResponseWriter, r *http.Request, mediaType string, feed opdsFeed) {
	feed.Xmlns = atomNS
	feed.XmlnsDC = dublinCoreNS
	feed.XmlnsOP = opdsNS
	w.Header().Set("Content-Type", mediaType+"; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Catalogs change whenever a book is added, and a reader refreshing
	// is exactly when it must not be served a stale list.
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
	_ = enc.Encode(feed)
	_ = enc.Close()
}

// opdsTitle keeps a feed valid when metadata is missing: Atom requires a
// title on every entry, and readers show a blank row for an empty one.
func opdsTitle(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func opdsTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
