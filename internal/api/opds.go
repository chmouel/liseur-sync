package api

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/cover"
	"github.com/chmouel/liseur-sync/internal/store"
)

// OPDS 1.2 lives under a versioned prefix so that a later OPDS 2.0
// surface can be added beside it rather than replacing it.
const opdsPrefix = "/opds/v1.2"

// opdsPageSize is how many books an acquisition feed carries, and it is
// deliberately smaller than defaultCatalogPageSize. An embedded catalog
// client parses a feed as it streams and keeps every entry it retains in
// RAM: one e-reader firmware budgets 64 entries by the size of its entry
// struct, then stores up to 500 characters of description, plus a title,
// an author and three links, for each of them on top. A page of 50
// Calibre books is a 100 KB feed and some 40 KB of that client's heap,
// which is enough for it to run out mid-parse and report a feed it could
// not read. Twenty entries of bounded text stay well inside that budget,
// and a reader pages to the rest through `next`.
const opdsPageSize = 20

// opdsSummaryLimit caps the description an entry carries. It is below the
// 500 characters such a client keeps anyway, and Calibre comments are
// routinely longer than either: one page of uncut descriptions was 70 KB
// of the 108 KB feed. Truncation happens on a rune boundary so the feed
// stays valid UTF-8.
const opdsSummaryLimit = 400

const (
	opdsNavigationType  = "application/atom+xml;profile=opds-catalog;kind=navigation"
	opdsAcquisitionType = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	opdsAcquisitionRel  = "http://opds-spec.org/acquisition"
	opdsImageRel        = "http://opds-spec.org/image"
	opdsThumbnailRel    = "http://opds-spec.org/image/thumbnail"
	atomNS              = "http://www.w3.org/2005/Atom"
	dublinCoreNS        = "http://purl.org/dc/terms/"
	opdsNS              = "http://opds-spec.org/2010/catalog"
)

// opdsFeed is the Atom document. Every feed the server emits is built as
// a struct and handed to encoding/xml: metadata is attacker-supplied
// (it comes out of EPUBs on somebody's disk), and an escaping mistake in a
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
	// Authors is plural in Atom, and every reader draws the first one.
	// An embedded catalog client takes its author from author/name only.
	Authors []opdsAuthor `xml:"author,omitempty"`
	// Dublin Core carries the bibliographic fields Atom has no room for.
	Publisher string     `xml:"dc:publisher,omitempty"`
	Issued    string     `xml:"dc:issued,omitempty"`
	Links     []opdsLink `xml:"link"`
}

type opdsText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

// HandleOPDSRoot serves the feed at /opds/v1.2: the folders this server
// watches, then every book those folders hold, newest first. The books
// are what makes the root the one URL a syncing reader needs: KOReader's
// catalog sync takes the first book of the configured feed as the newest
// and walks `next` until it meets the last one it downloaded, and it
// skips the folder entries because they carry no acquisition link. The
// folders appear on the first page only; later pages are books.
func (s *Server) HandleOPDSRoot(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	before, err := decodeCatalogCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feed := opdsFeed{
		ID:      "urn:liseur:opds:root",
		Title:   "Liseur",
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: opdsPrefix, Type: opdsAcquisitionType},
			{Rel: "start", Href: opdsPrefix, Type: opdsAcquisitionType},
		},
	}
	limit := opdsPageSize
	books, err := s.St.ListRecentLibraryBooks(r.Context(), tok.UserID, before, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	entries, err := s.opdsBookEntries(r.Context(), tok.UserID, books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	feed.Entries = append(feed.Entries, entries...)
	// Folders follow the books: KOReader's OPDS sync reads the first entry
	// as the newest book and crashes on a navigation entry there.
	if before == nil {
		folders, err := s.St.ListFolders(r.Context(), tok.UserID, "", maxCatalogPageSize)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "folder lookup failed")
			return
		}
		for _, f := range folders {
			href := opdsPrefix + "/folders/" + url.PathEscape(f.ID)
			feed.Entries = append(feed.Entries, opdsEntry{
				Title:   opdsTitle(f.Name, "Folder"),
				ID:      "urn:liseur:folder:" + f.ID,
				Updated: opdsTime(f.UpdatedAt),
				Content: &opdsText{Type: "text", Body: "Books in this folder"},
				Links: []opdsLink{
					{Rel: "subsection", Href: href, Type: opdsAcquisitionType},
				},
			})
		}
	}
	if len(books) == limit {
		last := books[len(books)-1]
		feed.Links = append(feed.Links, opdsLink{
			Rel:  "next",
			Type: opdsAcquisitionType,
			Href: opdsPrefix + "?cursor=" + url.QueryEscape(encodeCatalogCursor(
				store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID})),
		})
	}
	writeOPDS(w, r, opdsAcquisitionType, feed)
}

// HandleOPDSFolder serves the acquisition feed for one folder, newest
// first like the root, so a folder URL is a catalog a reader can sync
// too. It is paginated with the same cursor as the native catalog,
// exposed to the reader only as an opaque `next` link.
func (s *Server) HandleOPDSFolder(w http.ResponseWriter, r *http.Request) {
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
	before, err := decodeCatalogCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The folder is read first so the feed can be named after it: a feed
	// whose title is the word "Folder" is one a reader cannot tell apart
	// from the next one.
	folder, err := s.St.FolderByID(r.Context(), tok.UserID, folderID)
	if err != nil {
		writeCatalogError(w, err, "folder not found")
		return
	}
	limit := opdsPageSize
	books, err := s.St.ListRecentCatalogBooks(r.Context(), tok.UserID, folderID, before, limit)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "folder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}

	self := opdsPrefix + "/folders/" + url.PathEscape(folderID)
	feed := opdsFeed{
		ID:      "urn:liseur:folder:" + folderID,
		Title:   opdsTitle(folder.Name, "Folder"),
		Updated: opdsTime(time.Now()),
		Links: []opdsLink{
			{Rel: "self", Href: self, Type: opdsAcquisitionType},
			{Rel: "start", Href: opdsPrefix, Type: opdsAcquisitionType},
			{Rel: "up", Href: opdsPrefix, Type: opdsAcquisitionType},
			// Discovery, not decoration: a reader learns how to search
			// and how to browse from the feed it already has, so neither
			// URL has to be configured anywhere.
			{Rel: "search", Href: self + "/search.xml", Type: opdsSearchType,
				Title: "Search this folder"},
		},
	}
	for _, segment := range []string{"series", "contributors", "tags"} {
		// These browse the whole library rather than this folder
		// (ADR-0019), which is why they hang off the root prefix.
		feed.Links = append(feed.Links, opdsLink{
			Rel:  "http://opds-spec.org/facet",
			Href: opdsPrefix + "/entities/" + segment,
			Type: opdsNavigationType, Title: opdsKindTitles[segment],
		})
	}
	entries, err := s.opdsBookEntries(r.Context(), tok.UserID, books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	feed.Entries = entries
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

// opdsBookEntries renders one feed page, reading the page's contributors
// in one batched query rather than one per entry: a page is a bounded
// number of rows and must stay a bounded number of queries.
func (s *Server) opdsBookEntries(
	ctx context.Context, userID string, books []store.CatalogBook,
) ([]opdsEntry, error) {
	if len(books) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(books))
	for _, b := range books {
		ids = append(ids, b.ID)
	}
	rel, err := s.St.CatalogBookRelationsForBooks(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]opdsEntry, 0, len(books))
	for _, b := range books {
		out = append(out, opdsBookEntry(b, rel.Contributors[b.ID]))
	}
	return out, nil
}

func opdsBookEntry(b store.CatalogBook, contributors []store.BookContributor) opdsEntry {
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
		}, {
			// Readers fetch covers with the same Basic credential they
			// used for the feed, which is why they are reachable under
			// /opds as well as under /v1.
			//
			// `image` is the relation a reader draws, so it points at
			// the thumbnail and the thumbnail at the icon. A catalog is
			// a grid of small pictures, and the full variant is a
			// page-sized image that one e-reader client fetches by
			// preference into a few hundred kilobytes of free RAM.
			// The book page still serves it under /v1.
			Rel:  opdsImageRel,
			Href: opdsPrefix + "/books/" + url.PathEscape(b.ID) + "/cover",
			Type: cover.MediaType,
		}, {
			Rel:  opdsThumbnailRel,
			Href: opdsPrefix + "/books/" + url.PathEscape(b.ID) + "/cover?size=icon",
			Type: cover.MediaType,
		}},
	}
	// Roles are normalized on the way into the catalog, so picking the
	// authors out is a comparison rather than a guess.
	for _, c := range contributors {
		if c.Role == store.ContributorRoleAuthor {
			entry.Authors = append(entry.Authors, opdsAuthor{Name: c.Name})
		}
	}
	// A subtitle is worth showing even when there is no description, so
	// the two are merged into the one field readers reliably render.
	summary := b.Description
	if summary == "" {
		summary = b.Subtitle
	}
	if summary = opdsBounded(summary, opdsSummaryLimit); summary != "" {
		entry.Summary = &opdsText{Type: "text", Body: summary}
	}
	return entry
}

// opdsBounded shortens text to at most limit bytes without splitting a
// rune. Feed text is metadata out of somebody's EPUB, so it is UTF-8 that
// may be cut anywhere, including a few bytes into a multi-byte sequence.
func opdsBounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return strings.TrimRight(value[:cut], " \t\r\n") + "…"
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
