package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/cover"
	"github.com/chmouel/liseur-sync/internal/store"
)

// OPDS 2.0 (ADR-0065) is the JSON serialisation of the same catalog the
// 1.2 feeds describe. It is read through the same loaders, so a page here
// and the page at the matching 1.2 URL are always the same books.
const opds2Prefix = "/opds/v2"

const (
	opds2Type        = "application/opds+json"
	opds2AuthDocType = "application/opds-authentication+json"
	opds2BookType    = "http://schema.org/Book"
	opds2BasicAuth   = "http://opds-spec.org/auth/basic"
)

// The feed types mirror the OPDS 2.0 JSON schema closely enough that a
// field name here is the field name on the wire. Metadata is untrusted
// text out of EPUBs, so every document is built as a struct and handed
// to encoding/json, never assembled as a string.
type opds2Feed struct {
	Metadata     opds2FeedMetadata `json:"metadata"`
	Links        []opds2Link       `json:"links"`
	Navigation   *[]opds2Link      `json:"navigation,omitempty"`
	Publications *[]opds2Pub       `json:"publications,omitempty"`
	Groups       []opds2Group      `json:"groups,omitempty"`
}

type opds2FeedMetadata struct {
	Title        string `json:"title"`
	ItemsPerPage int    `json:"itemsPerPage,omitempty"`
}

type opds2Group struct {
	Metadata     opds2FeedMetadata `json:"metadata"`
	Links        []opds2Link       `json:"links,omitempty"`
	Publications []opds2Pub        `json:"publications"`
}

type opds2Link struct {
	Href       string          `json:"href"`
	Type       string          `json:"type,omitempty"`
	Rel        string          `json:"rel,omitempty"`
	Title      string          `json:"title,omitempty"`
	Templated  bool            `json:"templated,omitempty"`
	Properties *opds2LinkProps `json:"properties,omitempty"`
}

type opds2LinkProps struct {
	NumberOfItems int `json:"numberOfItems"`
}

type opds2Pub struct {
	Metadata opds2PubMetadata `json:"metadata"`
	Links    []opds2Link      `json:"links"`
	Images   []opds2Link      `json:"images"`
}

type opds2PubMetadata struct {
	Type        string              `json:"@type"`
	Identifier  string              `json:"identifier"`
	Title       string              `json:"title"`
	Subtitle    string              `json:"subtitle,omitempty"`
	Modified    string              `json:"modified,omitempty"`
	Published   string              `json:"published,omitempty"`
	Language    any                 `json:"language,omitempty"`
	Publisher   string              `json:"publisher,omitempty"`
	Description string              `json:"description,omitempty"`
	Author      []opds2Contributor  `json:"author,omitempty"`
	Translator  []opds2Contributor  `json:"translator,omitempty"`
	Editor      []opds2Contributor  `json:"editor,omitempty"`
	Illustrator []opds2Contributor  `json:"illustrator,omitempty"`
	Contributor []opds2Contributor  `json:"contributor,omitempty"`
	Subject     []opds2Subject      `json:"subject,omitempty"`
	BelongsTo   *opds2PubCollection `json:"belongsTo,omitempty"`
}

type opds2Contributor struct {
	Name     string      `json:"name"`
	Position *float64    `json:"position,omitempty"`
	Links    []opds2Link `json:"links,omitempty"`
}

type opds2Subject struct {
	Name string `json:"name"`
}

type opds2PubCollection struct {
	Series []opds2Contributor `json:"series"`
}

// opds2AuthDocument is the Authentication for OPDS 1.0 document. It is
// the body of every 401 under /opds/v2 and is served nowhere else: a
// client that could not open the catalog learns from the refusal itself
// what to ask its user for, and there is no open route to find.
type opds2AuthDocument struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Description    string            `json:"description"`
	Links          []opds2Link       `json:"links"`
	Authentication []opds2AuthMethod `json:"authentication"`
}

type opds2AuthMethod struct {
	Type   string          `json:"type"`
	Labels opds2AuthLabels `json:"labels"`
}

type opds2AuthLabels struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// opds2Challenge refuses an unauthenticated request with the Basic
// challenge header, which is all a Basic-only reader understands, and
// the Authentication Document as the body for a reader that reads it.
func opds2Challenge(w http.ResponseWriter, _ *http.Request, _ string) {
	auth.SetBasicChallenge(w)
	w.Header().Set("Content-Type", opds2AuthDocType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(opds2AuthDocument{
		ID:    "urn:liseur:opds:authentication",
		Title: auth.BasicRealm,
		Description: "Sign in with an API token that has the library-read scope. " +
			"Your account password does not work here.",
		Links: []opds2Link{{Rel: "logo", Href: "/ui/static/icon.png", Type: "image/png"}},
		Authentication: []opds2AuthMethod{{
			Type: opds2BasicAuth,
			Labels: opds2AuthLabels{
				Login:    "Token name",
				Password: "Token secret",
			},
		}},
	})
}

// HandleOPDS2Root serves /opds/v2: the ways into the catalog as
// navigation, and the newest books as a group whose own link is the
// paginated feed. The group is one page only; a reader that syncs a
// catalog by walking `next` is pointed at /opds/v2/new instead.
func (s *Server) HandleOPDS2Root(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	// The root is the first page by definition; a cursor belongs to /new.
	if r.URL.Query().Has("cursor") {
		writeError(w, http.StatusBadRequest, "the root feed is not paginated; use "+opds2Prefix+"/new")
		return
	}
	books, _, ok := s.opdsRecentPage(w, r, tok.UserID)
	if !ok {
		return
	}
	pubs, err := s.opds2Publications(r.Context(), tok.UserID, books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	folders, err := s.St.ListFolders(r.Context(), tok.UserID, "", maxCatalogPageSize)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "folder lookup failed")
		return
	}
	nav := make([]opds2Link, 0, len(folders)+len(opdsShelves)+len(opdsEntitySegments))
	for _, f := range folders {
		nav = append(nav, opds2Link{
			Href: opds2Prefix + "/folders/" + url.PathEscape(f.ID),
			Type: opds2Type, Title: opdsTitle(f.Name, "Folder"),
		})
	}
	for _, sh := range opdsShelves {
		nav = append(nav, opds2Link{
			Href: opds2Prefix + "/shelves/" + sh.slug, Type: opds2Type, Title: sh.title,
		})
	}
	for _, segment := range opdsEntitySegments {
		nav = append(nav, opds2Link{
			Href: opds2Prefix + "/entities/" + segment, Type: opds2Type,
			Title: opdsKindTitles[segment],
		})
	}
	feed := opds2Feed{
		Metadata: opds2FeedMetadata{Title: "Liseur"},
		Links: []opds2Link{
			{Rel: "self", Href: opds2Prefix, Type: opds2Type},
			{Rel: "start", Href: opds2Prefix, Type: opds2Type},
		},
		Navigation: &nav,
	}
	// The schema wants every collection non-empty, and an empty group is
	// a heading over nothing, so a library with no books has no group.
	if len(pubs) > 0 {
		feed.Groups = []opds2Group{{
			Metadata:     opds2FeedMetadata{Title: "Newest", ItemsPerPage: opdsPageSize},
			Links:        []opds2Link{{Rel: "self", Href: opds2Prefix + "/new", Type: opds2Type}},
			Publications: pubs,
		}}
	}
	writeOPDS2(w, r, feed)
}

// HandleOPDS2New serves every book the reader can see, newest first,
// with the same keyset cursor as the 1.2 root feed.
func (s *Server) HandleOPDS2New(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	books, next, ok := s.opdsRecentPage(w, r, tok.UserID)
	if !ok {
		return
	}
	self := opds2Prefix + "/new"
	s.writeOPDS2Publications(w, r, tok.UserID, "Newest", books, []opds2Link{
		{Rel: "self", Href: self, Type: opds2Type},
		{Rel: "start", Href: opds2Prefix, Type: opds2Type},
		{Rel: "up", Href: opds2Prefix, Type: opds2Type},
	}, opds2Next(self, "cursor", next))
}

// HandleOPDS2Folder serves one folder newest first, with the folder's
// search offered as a URI template.
func (s *Server) HandleOPDS2Folder(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	folder, books, next, ok := s.opdsFolderPage(w, r, tok.UserID)
	if !ok {
		return
	}
	self := opds2Prefix + "/folders/" + url.PathEscape(folder.ID)
	s.writeOPDS2Publications(w, r, tok.UserID, opdsTitle(folder.Name, "Folder"), books, []opds2Link{
		{Rel: "self", Href: self, Type: opds2Type},
		{Rel: "start", Href: opds2Prefix, Type: opds2Type},
		{Rel: "up", Href: opds2Prefix, Type: opds2Type},
		{Rel: "search", Href: self + "/search{?query}", Type: opds2Type,
			Templated: true, Title: "Search this folder"},
	}, opds2Next(self, "cursor", next))
}

// HandleOPDS2Search serves a folder search. Like its 1.2 twin it is one
// unpaged page of the best matches.
func (s *Server) HandleOPDS2Search(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	folderID, text, books, ok := s.opdsSearchResults(w, r, tok.UserID, "query")
	if !ok {
		return
	}
	folderHref := opds2Prefix + "/folders/" + url.PathEscape(folderID)
	s.writeOPDS2Publications(w, r, tok.UserID, "Search results", books, []opds2Link{
		{Rel: "self", Href: folderHref + "/search?query=" + url.QueryEscape(text), Type: opds2Type},
		{Rel: "start", Href: opds2Prefix, Type: opds2Type},
		{Rel: "up", Href: folderHref, Type: opds2Type},
	}, nil)
}

// HandleOPDS2Shelf serves one of the reader's own shelves (ADR-0064).
func (s *Server) HandleOPDS2Shelf(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.TokenFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	shelf, books, next, ok := s.opdsShelfPage(w, r, tok.UserID)
	if !ok {
		return
	}
	self := opds2Prefix + "/shelves/" + shelf.slug
	s.writeOPDS2Publications(w, r, tok.UserID, shelf.title, books, []opds2Link{
		{Rel: "self", Href: self, Type: opds2Type},
		{Rel: "start", Href: opds2Prefix, Type: opds2Type},
		{Rel: "up", Href: opds2Prefix, Type: opds2Type},
	}, opds2Next(self, "offset", next))
}

// HandleOPDS2Entities lists the library's series, contributors or tags
// as navigation, each with the number of books behind it.
func (s *Server) HandleOPDS2Entities(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	segment, entities, nextAfter, ok := s.opdsEntitiesPage(w, r)
	if !ok {
		return
	}
	self := opds2Prefix + "/entities/" + segment
	nav := make([]opds2Link, 0, len(entities))
	for _, e := range entities {
		nav = append(nav, opds2Link{
			Href: self + "/" + url.PathEscape(e.ID), Type: opds2Type,
			Title:      opdsTitle(e.Name, "Untitled"),
			Properties: &opds2LinkProps{NumberOfItems: e.BookCount},
		})
	}
	links := []opds2Link{
		{Rel: "self", Href: self, Type: opds2Type},
		{Rel: "start", Href: opds2Prefix, Type: opds2Type},
		{Rel: "up", Href: opds2Prefix, Type: opds2Type},
	}
	links = append(links, opds2Next(self, "after", nextAfter)...)
	writeOPDS2(w, r, opds2Feed{
		Metadata:   opds2FeedMetadata{Title: opdsKindTitles[segment], ItemsPerPage: opdsPageSize},
		Links:      links,
		Navigation: &nav,
	})
}

// HandleOPDS2EntityBooks serves the books claiming one entity; a series
// comes back in reading order.
func (s *Server) HandleOPDS2EntityBooks(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	segment, entity, books, next, ok := s.opdsEntityBooksPage(w, r)
	if !ok {
		return
	}
	kindHref := opds2Prefix + "/entities/" + segment
	self := kindHref + "/" + url.PathEscape(entity.ID)
	s.writeOPDS2Publications(w, r, readerID(r), opdsTitle(entity.Name, "Untitled"), books, []opds2Link{
		{Rel: "self", Href: self, Type: opds2Type},
		{Rel: "start", Href: opds2Prefix, Type: opds2Type},
		{Rel: "up", Href: kindHref, Type: opds2Type},
	}, opds2Next(self, "cursor", next))
}

// HandleOPDS2BookAudiobook downloads the audiobook linked to a book,
// under the same grant and alignment rules as the native audio route:
// a reader is offered it only when both folders are granted.
func (s *Server) HandleOPDS2BookAudiobook(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.TokenFrom(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.serveBookAudio(w, r, readerID(r), r.PathValue("id"), "", "", true)
}

func opds2Next(self, param, value string) []opds2Link {
	if value == "" {
		return nil
	}
	return []opds2Link{{Rel: "next", Href: self + "?" + param + "=" + url.QueryEscape(value), Type: opds2Type}}
}

func (s *Server) writeOPDS2Publications(
	w http.ResponseWriter, r *http.Request, userID, title string,
	books []store.CatalogBook, links, next []opds2Link,
) {
	pubs, err := s.opds2Publications(r.Context(), userID, books)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog listing failed")
		return
	}
	writeOPDS2(w, r, opds2Feed{
		Metadata:     opds2FeedMetadata{Title: title, ItemsPerPage: opdsPageSize},
		Links:        append(links, next...),
		Publications: &pubs,
	})
}

// opds2Publications renders one page with two batched reads, relations
// and audiobook availability, whatever the page size.
func (s *Server) opds2Publications(
	ctx context.Context, userID string, books []store.CatalogBook,
) ([]opds2Pub, error) {
	out := make([]opds2Pub, 0, len(books))
	if len(books) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(books))
	for _, b := range books {
		ids = append(ids, b.ID)
	}
	rel, err := s.St.CatalogBookRelationsForBooks(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	var audio map[string]bool
	if _, ok := s.Files.(AudiobookFiles); ok {
		if audio, err = s.St.PlayableAudiobookBookIDs(ctx, userID, ids); err != nil {
			return nil, err
		}
	}
	for _, b := range books {
		out = append(out, opds2Publication(b, rel, audio[b.ID]))
	}
	return out, nil
}

func opds2Publication(b store.CatalogBook, rel store.CatalogBookRelations, audiobook bool) opds2Pub {
	bookHref := opds2Prefix + "/books/" + url.PathEscape(b.ID)
	md := opds2PubMetadata{
		Type:        opds2BookType,
		Identifier:  opds2Identifier(b.ID, rel.Identifiers[b.ID]),
		Title:       opdsTitle(b.Title, "Untitled"),
		Subtitle:    b.Subtitle,
		Modified:    opdsTime(b.UpdatedAt),
		Published:   b.PublishedDate,
		Publisher:   b.Publisher,
		Description: opdsBounded(b.Description, opdsSummaryLimit),
	}
	switch langs := rel.Languages[b.ID]; len(langs) {
	case 0:
	case 1:
		md.Language = langs[0]
	default:
		md.Language = langs
	}
	for _, c := range rel.Contributors[b.ID] {
		person := opds2Contributor{Name: c.Name}
		if c.ContributorID != "" {
			person.Links = []opds2Link{{
				Href: opds2Prefix + "/entities/contributors/" + url.PathEscape(c.ContributorID),
				Type: opds2Type,
			}}
		}
		switch c.Role {
		case store.ContributorRoleAuthor:
			md.Author = append(md.Author, person)
		case "translator":
			md.Translator = append(md.Translator, person)
		case "editor":
			md.Editor = append(md.Editor, person)
		case "illustrator":
			md.Illustrator = append(md.Illustrator, person)
		default:
			md.Contributor = append(md.Contributor, person)
		}
	}
	for _, tag := range rel.Tags[b.ID] {
		md.Subject = append(md.Subject, opds2Subject{Name: tag})
	}
	if series := rel.Series[b.ID]; len(series) > 0 {
		md.BelongsTo = &opds2PubCollection{}
		for _, sr := range series {
			md.BelongsTo.Series = append(md.BelongsTo.Series, opds2Contributor{
				Name:     opdsTitle(sr.Name, "Untitled"),
				Position: sr.Position,
				Links: []opds2Link{{
					Href: opds2Prefix + "/entities/series/" + url.PathEscape(sr.SeriesID),
					Type: opds2Type,
				}},
			})
		}
	}
	links := []opds2Link{{
		Rel:  opdsAcquisitionRel,
		Href: bookHref + "/download",
		Type: downloadMediaType(b.MediaType),
	}}
	if audiobook {
		links = append(links, opds2Link{
			Rel:   opdsAcquisitionRel,
			Href:  bookHref + "/audiobook",
			Type:  "audio/mp4",
			Title: "Audiobook",
		})
	}
	return opds2Pub{
		Metadata: md,
		Links:    links,
		// The first image is what a grid draws, so it is the thumbnail;
		// the full cover follows for a reader that shows a book page.
		Images: []opds2Link{
			{Href: bookHref + "/cover", Type: cover.MediaType},
			{Href: bookHref + "/cover?size=full", Type: cover.MediaType},
		},
	}
}

var (
	opds2ISBN = regexp.MustCompile(`^(?:[0-9]{9}[0-9X]|[0-9]{13})$`)
	opds2UUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// opds2Identifier picks a URI for the publication. An ISBN or UUID from
// the book's own metadata lets a reader match it to a copy it already
// holds; anything that does not parse as one falls back to the catalog
// ID, because the field is a URI and metadata is not trusted to be one.
func opds2Identifier(bookID string, ids []store.BookIdentifier) string {
	for _, want := range []string{"isbn", "uuid"} {
		for _, id := range ids {
			if id.Scheme != want {
				continue
			}
			v := strings.ToLower(strings.TrimSpace(id.Value))
			v = strings.TrimPrefix(v, "urn:"+want+":")
			if want == "isbn" {
				v = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(v))
				if opds2ISBN.MatchString(v) {
					return "urn:isbn:" + v
				}
			} else if opds2UUID.MatchString(v) {
				return "urn:uuid:" + v
			}
		}
	}
	return "urn:liseur:book:" + bookID
}

// opds2Alternate is the OPDS 2.0 URL of the 1.2 feed this request asked
// for, so a 1.2 feed can point a JSON-capable reader at its twin. The
// 1.2 root is both the navigation and the newest-first walk; its later
// pages map to /new, which is where the walk lives in 2.0.
func opds2Alternate(r *http.Request) string {
	rest := strings.TrimPrefix(r.URL.Path, opdsPrefix)
	q := r.URL.Query()
	if rest == "" || rest == "/" {
		if c := q.Get("cursor"); c != "" {
			return opds2Prefix + "/new?cursor=" + url.QueryEscape(c)
		}
		return opds2Prefix
	}
	if path.Base(rest) == "search" && q.Has("q") {
		q.Set("query", q.Get("q"))
		q.Del("q")
	}
	alt := opds2Prefix + rest
	if enc := q.Encode(); enc != "" {
		alt += "?" + enc
	}
	return alt
}

func writeOPDS2(w http.ResponseWriter, r *http.Request, feed opds2Feed) {
	w.Header().Set("Content-Type", opds2Type)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(feed)
}
