//go:build linux

package api

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// parsedFeed is a deliberately loose view of the response: tests assert on
// what a reader actually reads, not on the struct the server serialised
// from. Round-tripping through the encoder is the point.
type parsedFeed struct {
	XMLName xml.Name `xml:"feed"`
	ID      string   `xml:"id"`
	Title   string   `xml:"title"`
	Links   []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
		Type string `xml:"type,attr"`
	} `xml:"link"`
	Entries []struct {
		Title     string `xml:"title"`
		ID        string `xml:"id"`
		Summary   string `xml:"summary"`
		Publisher string `xml:"publisher"`
		Links     []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
			Type string `xml:"type,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

func (f *folderFixture) opds(t *testing.T, path, user, pass string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+path, nil)
	if pass != "" || user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func parseFeed(t *testing.T, raw []byte) parsedFeed {
	t.Helper()
	var feed parsedFeed
	if err := xml.Unmarshal(raw, &feed); err != nil {
		t.Fatalf("feed is not valid XML: %v\n%s", err, raw)
	}
	return feed
}

func linkHref(t *testing.T, links []struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr"`
}, rel string,
) string {
	t.Helper()
	for _, l := range links {
		if l.Rel == rel {
			return l.Href
		}
	}
	return ""
}

// TestOPDSWalksFromRootToBytes is the KOReader path end to end: start at
// the root with nothing but a password, follow links, download the book.
// A reader that cannot do this walk cannot use the server at all.
func TestOPDSWalksFromRootToBytes(t *testing.T) {
	f := newFolderFixture(t)
	epubBody := makeEPUB(t, "Feed Walked Book", "", bytes.Repeat([]byte("opds-epub"), 64))
	_, digest := f.writeBook(t, "opds.epub", epubBody)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, raw := f.opds(t, "/opds/v1.2", "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root: %d %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "kind=navigation") {
		t.Fatalf("root content-type = %q", ct)
	}
	root := parseFeed(t, raw)
	if len(root.Entries) != 1 {
		t.Fatalf("root entries = %+v", root.Entries)
	}
	folderHref := linkHref(t, root.Entries[0].Links, "subsection")
	if folderHref == "" {
		t.Fatalf("no subsection link: %+v", root.Entries[0])
	}

	resp, raw = f.opds(t, folderHref, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("folder feed: %d %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "kind=acquisition") {
		t.Fatalf("folder content-type = %q", ct)
	}
	folder := parseFeed(t, raw)
	if len(folder.Entries) != 1 {
		t.Fatalf("folder entries = %+v", folder.Entries)
	}
	entry := folder.Entries[0]
	if entry.Title != "Feed Walked Book" {
		t.Fatalf("entry title = %q", entry.Title)
	}
	acq := linkHref(t, entry.Links, opdsAcquisitionRel)
	if acq == "" {
		t.Fatalf("no acquisition link: %+v", entry)
	}

	resp, got := f.opds(t, acq, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("acquisition: %d %s", resp.StatusCode, got)
	}
	if !bytes.Equal(got, epubBody) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(epubBody))
	}
	if want := `W/"` + digest + `"`; resp.Header.Get("ETag") != want {
		t.Fatalf("etag = %q want %q", resp.Header.Get("ETag"), want)
	}
}

// TestOPDSRejectsAccountPasswords pins the rule that the catalog password
// is a token secret and never the account password. A reader stores its
// credential in plain text on the device; handing it the account password
// would put the whole account on an e-reader's filesystem.
func TestOPDSRejectsAccountPasswords(t *testing.T) {
	f := newFolderFixture(t)
	const password = "correct horse battery staple"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	u := store.User{
		ID: "u-opds-pw", Name: "reader", Argon2Hash: hash,
		Timezone: "UTC", CreatedAt: time.Now().UTC(),
	}
	if err := f.st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	// The password is genuinely this user's: Login accepts it.
	if _, err := auth.NewService(f.st).Login(t.Context(), u.Name, password); err != nil {
		t.Fatalf("password should be valid for login: %v", err)
	}

	resp, raw := f.opds(t, "/opds/v1.2", u.Name, password)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("account password was accepted: %d %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("no Basic challenge: %q", got)
	}
}

// TestOPDSRefusesCredentialsOutsideBasicAuth covers the two ways a reader
// might try to smuggle a secret: a query parameter, which would be logged
// by every proxy in between, and a bearer header, which no OPDS client
// sends and which would quietly widen the surface if it worked.
func TestOPDSRefusesCredentialsOutsideBasicAuth(t *testing.T) {
	f := newFolderFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, tc := range []struct {
		name string
		mut  func(*http.Request)
	}{
		{"query string", func(r *http.Request) {
			q := r.URL.Query()
			q.Set("token", read)
			q.Set("password", read)
			r.URL.RawQuery = q.Encode()
		}},
		{"bearer header", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+read)
		}},
		{"no credential", func(*http.Request) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, f.ts.URL+"/opds/v1.2", nil)
			tc.mut(req)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}

// TestOPDSAcceptsTheTokenNameAsUsername covers the two usernames the ADR
// promises, and rejects a third. The username carries no authority, but a
// reader configured against the wrong account should fail visibly rather
// than browse a catalog it did not expect.
func TestOPDSAcceptsTheTokenNameAsUsername(t *testing.T) {
	f := newFolderFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, tc := range []struct {
		user string
		want int
	}{
		{"token", http.StatusOK},
		{"test-device", http.StatusOK}, // the name f.mintToken gives the token.
		{"", http.StatusOK},
		{"someone-else", http.StatusUnauthorized},
	} {
		resp, raw := f.opds(t, "/opds/v1.2", tc.user, read)
		if resp.StatusCode != tc.want {
			t.Fatalf("user %q: %d want %d (%s)", tc.user, resp.StatusCode, tc.want, raw)
		}
	}
}

// TestOPDSRequiresLibraryReadScope: a sync-only token is a valid
// credential, so it must fail with 403 and no challenge — re-prompting
// the user for a password they already got right helps nobody.
func TestOPDSRequiresLibraryReadScope(t *testing.T) {
	f := newFolderFixture(t)
	sync := f.mintToken(t, f.user.ID, store.ScopeSync)

	resp, raw := f.opds(t, "/opds/v1.2", "token", sync)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("sync token reached the catalog: %d %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != "" {
		t.Fatalf("challenged a valid credential: %q", got)
	}
}

// TestOPDSPagesWithoutLosingBooks walks the `next` links exactly as a
// reader does and checks every book appears once. A feed that silently
// drops books is worse than one that fails.
func TestOPDSPagesWithoutLosingBooks(t *testing.T) {
	f := newFolderFixture(t)
	const total = opdsPageSize + 3
	want := map[string]bool{}
	for i := range total {
		id, _ := f.publish(t, fmt.Sprintf("p%02d", i), []byte(fmt.Sprintf("body-%d", i)))
		want["urn:liseur:book:"+id] = false
	}
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	path := "/opds/v1.2/folders/" + f.folder.ID
	pages := 0
	for path != "" {
		pages++
		if pages > 10 {
			t.Fatal("feed never ends")
		}
		resp, raw := f.opds(t, path, "token", read)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page %d: %d %s", pages, resp.StatusCode, raw)
		}
		feed := parseFeed(t, raw)
		for _, e := range feed.Entries {
			seen, known := want[e.ID]
			if !known {
				t.Fatalf("unexpected entry %q", e.ID)
			}
			if seen {
				t.Fatalf("entry %q served twice", e.ID)
			}
			want[e.ID] = true
		}
		path = linkHref(t, feed.Links, "next")
	}
	if pages != 2 {
		t.Fatalf("paged %d times, want 2", pages)
	}
	for id, seen := range want {
		if !seen {
			t.Fatalf("book %q never appeared", id)
		}
	}
}

// TestOPDSEscapesHostileMetadata: titles come from the folder's own
// files, so they are attacker-controlled the moment somebody can write
// into a watched directory. Many readers render feed text as HTML. The
// encoder must escape it, and the feed must still parse.
func TestOPDSEscapesHostileMetadata(t *testing.T) {
	f := newFolderFixture(t)
	const evil = `</title><script>alert("xss")</script>& "quoted" 'single'`
	f.publishAs(t, "hostile", evil, []byte("body"))

	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	resp, raw := f.opds(t, "/opds/v1.2/folders/"+f.folder.ID, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feed: %d %s", resp.StatusCode, raw)
	}
	if bytes.Contains(raw, []byte("<script>")) {
		t.Fatalf("unescaped markup in feed:\n%s", raw)
	}
	feed := parseFeed(t, raw)
	if len(feed.Entries) != 1 || feed.Entries[0].Title != evil {
		t.Fatalf("title did not round-trip: %+v", feed.Entries)
	}
}

// TestOPDSKeepsUnknownPathsOut: the root feed is registered on an exact
// pattern, so a typo is a 404 and not a confusingly empty catalog.
func TestOPDSKeepsUnknownPathsOut(t *testing.T) {
	f := newFolderFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, path := range []string{"/opds/v1.2/nonsense", "/opds/v1.2/folders"} {
		resp, _ := f.opds(t, path, "token", read)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, resp.StatusCode)
		}
	}
	// The two spellings of the root both work, because readers differ on
	// whether they keep the trailing slash.
	for _, path := range []string{"/opds/v1.2", "/opds/v1.2/"} {
		resp, raw := f.opds(t, path, "token", read)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d %s", path, resp.StatusCode, raw)
		}
	}
}

// TestOPDSRejectsAMalformedCursor: a reader that mangles a `next` link
// must get a 4xx, never a 5xx.
func TestOPDSRejectsAMalformedCursor(t *testing.T) {
	f := newFolderFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, cursor := range []string{"!!!", "eyJ0Ijoibm90LWEtdGltZSJ9", "e30"} {
		resp, _ := f.opds(t,
			"/opds/v1.2/folders/"+f.folder.ID+"?cursor="+cursor, "token", read)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("cursor %q = %d, want 400", cursor, resp.StatusCode)
		}
	}
}

// A reader renders covers straight from the feed, using the same Basic
// credential it used to fetch it. Without both the links and a route that
// accepts that credential, every image in an acquisition feed is a broken
// one.
func TestOPDSEntriesCarryCoverLinks(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publish(t, "opds-cover", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	resp, raw := f.opds(t, "/opds/v1.2/folders/"+f.folder.ID, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("acquisition feed: %d %s", resp.StatusCode, raw)
	}
	var feed parsedFeed
	if err := xml.Unmarshal(raw, &feed); err != nil {
		t.Fatal(err)
	}

	var thumbnail, full string
	for _, entry := range feed.Entries {
		for _, link := range entry.Links {
			switch link.Rel {
			case "http://opds-spec.org/image/thumbnail":
				thumbnail = link.Href
			case "http://opds-spec.org/image":
				full = link.Href
			}
		}
	}
	if thumbnail == "" || full == "" {
		t.Fatalf("feed entry has no cover links: %+v", feed.Entries)
	}
	if !strings.Contains(thumbnail, bookID) {
		t.Fatalf("thumbnail link %q is not this book's", thumbnail)
	}
	// Neither variant is the page-sized one. A catalog is a grid of small
	// pictures, and the client that reads these feeds fetches `image` by
	// preference: handing it a 1000-pixel cover spends hundreds of
	// kilobytes of a device's RAM on a thumbnail's worth of pixels.
	if strings.Contains(full, "size=full") || strings.Contains(thumbnail, "size=full") {
		t.Fatalf("feed advertises the page-sized cover: image=%q thumbnail=%q", full, thumbnail)
	}

	// The links must be fetchable with the feed's own credential.
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		f.ts.URL+thumbnail, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("token", read)
	fetched, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer fetched.Body.Close()
	if fetched.StatusCode != http.StatusOK {
		t.Fatalf("fetching an advertised cover: %d", fetched.StatusCode)
	}
	if got := fetched.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q", got)
	}
}

// crossPointFeed is what an embedded catalog client extracts from a feed:
// a feed title, a list of entries, and whether it had to stop collecting.
type crossPointFeed struct {
	title     string
	entries   []crossPointEntry
	truncated bool
}

type crossPointEntry struct {
	title     string
	author    string
	href      string
	id        string
	summary   string
	publisher string
}

// crossPointEntryLimit is the entry ceiling of the client this test
// mirrors: it reserves storage for a page in RAM and keeps a margin, so a
// feed carrying more entries than this is silently cut short.
const crossPointEntryLimit = 62

// crossPointDescriptionLimit is how much of a summary that client keeps.
const crossPointDescriptionLimit = 500

// parseLikeCrossPoint walks the feed the way an embedded client's expat
// handler does: prefixes are ignored rather than resolved (so `dc:publisher`
// and `publisher` are the same element), the author comes from
// author/name, the acquisition link is the entry's href, and collecting
// stops at the entry ceiling. It is deliberately not encoding/xml's
// struct unmarshalling: the client has no schema, and a test that used one
// would not notice an element the client cannot reach.
func parseLikeCrossPoint(t *testing.T, raw []byte) crossPointFeed {
	t.Helper()
	var (
		feed         crossPointFeed
		entry        crossPointEntry
		inEntry      bool
		collect      bool
		inFeedTitle  bool
		inTitle      bool
		inAuthor     bool
		inAuthorName bool
		inID         bool
		inSummary    bool
		inPublisher  bool
		text         strings.Builder
	)
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("feed is not well-formed XML: %v\n%s", err, raw)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			name := el.Name.Local
			switch {
			case name == "entry":
				inEntry = true
				collect = len(feed.entries) < crossPointEntryLimit
				feed.truncated = feed.truncated || !collect
				entry = crossPointEntry{}
				inTitle, inAuthor, inAuthorName, inID = false, false, false, false
				inSummary, inPublisher = false, false
			case inEntry && inSummary:
				// Nested markup inside a description is not an entry field.
			case inEntry && inPublisher:
			case name == "link":
				if !inEntry {
					continue
				}
				var rel string
				for _, attr := range el.Attr {
					switch attr.Name.Local {
					case "rel":
						rel = attr.Value
					case "href":
						if strings.Contains(rel, "acquisition") || strings.Contains(rel, "atom+xml") {
							if entry.href == "" {
								entry.href = attr.Value
							}
						}
					}
				}
			case !inEntry && name == "title":
				inFeedTitle = true
				text.Reset()
			case !collect:
			case name == "title":
				inTitle = true
				text.Reset()
			case name == "author":
				inAuthor = true
			case inAuthor && name == "name":
				inAuthorName = true
				text.Reset()
			case name == "id":
				inID = true
				text.Reset()
			case name == "summary" || name == "content":
				inSummary = true
				text.Reset()
			case name == "publisher":
				inPublisher = true
				text.Reset()
			}
		case xml.CharData:
			if inEntry {
				if inTitle || inAuthorName || inID || inSummary || inPublisher {
					text.Write(el)
				}
				continue
			}
			if inFeedTitle {
				text.Write(el)
			}
		case xml.EndElement:
			name := el.Name.Local
			switch {
			case name == "entry" && inEntry:
				if collect && entry.title != "" && entry.href != "" {
					feed.entries = append(feed.entries, entry)
				}
				inEntry, collect = false, false
			case !inEntry && name == "title" && inFeedTitle:
				feed.title = text.String()
				inFeedTitle = false
			case inEntry && name == "title" && inTitle:
				entry.title = text.String()
				inTitle = false
			case inEntry && name == "author":
				inAuthor = false
			case inEntry && name == "name" && inAuthorName:
				entry.author = text.String()
				inAuthorName = false
			case inEntry && name == "id" && inID:
				entry.id = text.String()
				inID = false
			case inEntry && (name == "summary" || name == "content") && inSummary:
				entry.summary = text.String()
				inSummary = false
			case inEntry && name == "publisher" && inPublisher:
				entry.publisher = text.String()
				inPublisher = false
			}
		}
	}
	return feed
}

// TestOPDSCrossPointLoadsTheFolderFeed pins what an embedded catalog
// client needs from an acquisition feed, on the feed shape that client
// actually fails on: a Calibre folder whose books carry long comments.
//
// It fails today in three ways: the entries have no author, each summary
// carries the whole comment (tens of kilobytes per feed, most of it
// thrown away by the client's own 500-character cap), and the page is
// sized for a browser rather than for a device holding every entry in
// RAM. The budget below is the regression guard: a feed that grows past
// it is a feed that client reports as unparseable.
func TestOPDSCrossPointLoadsTheFolderFeed(t *testing.T) {
	f := newFolderFixture(t)
	comment := strings.Repeat("Un commentaire de Calibre très long, avec de la ponctuation. ", 200)
	total := opdsPageSize + 5
	for i := range total {
		f.publish(t, fmt.Sprintf("crosspoint-%02d", i),
			makeEPUBWithDescription(t, fmt.Sprintf("Livre %02d", i),
				fmt.Sprintf("Auteur %02d", i), comment, fmt.Sprintf("body-%d", i)))
	}
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, raw := f.opds(t, "/opds/v1.2/folders/"+f.folder.ID, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("folder feed: %d %s", resp.StatusCode, raw)
	}

	feed := parseLikeCrossPoint(t, raw)
	if feed.truncated {
		t.Fatalf("feed carries more than the %d entries a client can keep", crossPointEntryLimit)
	}
	if len(feed.entries) != opdsPageSize {
		t.Fatalf("feed entries = %d, want %d", len(feed.entries), opdsPageSize)
	}
	if feed.title != "Books" {
		t.Fatalf("feed title = %q, want the folder's own name", feed.title)
	}
	for _, entry := range feed.entries {
		if entry.author == "" {
			t.Fatalf("entry %q carries no author/name", entry.title)
		}
		if entry.href == "" {
			t.Fatalf("entry %q has no acquisition link", entry.title)
		}
		if len(entry.summary) > crossPointDescriptionLimit {
			t.Fatalf("entry %q summary is %d bytes, past the client's own %d cap",
				entry.title, len(entry.summary), crossPointDescriptionLimit)
		}
	}
	// The byte budget is roughly two orders of magnitude of headroom over
	// what this page needs, and far under the 100 KB a browser-sized page
	// reached: what is being pinned is that the feed stays small enough to
	// parse on a device with a few hundred kilobytes of RAM.
	const budget = 24 << 10
	if len(raw) > budget {
		t.Fatalf("folder feed is %d bytes, over the %d-byte budget", len(raw), budget)
	}
}

// makeEPUBWithDescription builds a publication whose OPF carries a
// dc:description and a dc:creator, the two fields a feed page renders and
// the two that decide its size: Calibre writes the folder's whole comment
// into dc:description, and a shelf of them is what a feed budget is for.
func makeEPUBWithDescription(t *testing.T, title, author, description, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, content string, method uint16) {
		t.Helper()
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, content); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OPS/book.opf"
 media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate)
	add("OPS/book.opf", `<package xmlns="http://www.idpf.org/2007/opf">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
 <dc:title>`+xmlEscape(title)+`</dc:title>
 <dc:creator>`+xmlEscape(author)+`</dc:creator>
 <dc:description>`+xmlEscape(description)+`</dc:description>
</metadata>
<manifest><item href="nav.xhtml" media-type="application/xhtml+xml"
 properties="nav"/></manifest></package>`, zip.Deflate)
	add("OPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><nav/>
<!-- `+body+` --></body></html>`, zip.Deflate)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestOPDSLogsScopeRefusal: a token without library-read is the OPDS
// misconfiguration people actually hit, and it is refused by the scope
// check rather than by authentication. The log has to name the credential
// anyway: an operator reading "insufficient scope" needs to know which
// token to widen, and a 403 with an empty token id says nothing.
func TestOPDSLogsScopeRefusal(t *testing.T) {
	f := newFolderFixture(t)
	narrow := f.mintToken(t, f.user.ID, store.ScopeSync)
	toks, err := f.st.ListTokens(t.Context(), f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wantID string
	for _, tok := range toks {
		if !tok.Scopes.Allows(store.ScopeLibraryRead) {
			wantID = tok.ID
		}
	}
	if wantID == "" {
		t.Fatal("no token without library-read was minted")
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	resp, _ := f.opds(t, "/opds/v1.2", "token", narrow)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("narrow token: %d", resp.StatusCode)
	}
	logged := buf.String()
	for _, want := range []string{"status=403", "token_id=" + wantID} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log is missing %q:\n%s", want, logged)
		}
	}
	if strings.Contains(logged, narrow) {
		t.Fatalf("the secret leaked into the log:\n%s", logged)
	}
}

// TestOPDSLogsRefusedRequests: an e-reader reports "parse feed failed"
// for every non-2xx answer and says nothing about which one it got, so the
// server has to. What is logged is the status, the route pattern and the
// credential's id; what is never logged is the secret, the folder id or
// the book id, because this log is kept and a catalog of what somebody
// reads is not the server's to publish.
func TestOPDSLogsRefusedRequests(t *testing.T) {
	f := newFolderFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// A wrong password: refused by the Basic middleware, before any
	// handler ran.
	resp, _ := f.opds(t, "/opds/v1.2/folders/"+f.folder.ID, "token", "not-the-secret")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
	// A valid credential on a folder it was not granted: refused by the
	// store, for a request the reader made on purpose.
	if err := f.st.UnassignUserFolder(t.Context(), f.user.ID, f.folder.ID); err != nil {
		t.Fatalf("revoking the grant: %v", err)
	}
	resp, _ = f.opds(t, "/opds/v1.2/folders/"+f.folder.ID, "token", read)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked grant: %d", resp.StatusCode)
	}

	logged := buf.String()
	for _, want := range []string{
		"status=401",
		"status=404",
		"route=\"GET /opds/v1.2/folders/{folder}\"",
		// The credential that was refused is named by id, so an operator
		// can revoke the one that is misconfigured instead of every one.
		"token_id=",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log is missing %q:\n%s", want, logged)
		}
	}
	var refusedWithCredential string
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, "status=404") {
			refusedWithCredential = line
		}
	}
	if !strings.Contains(refusedWithCredential, "token_id=") ||
		strings.Contains(refusedWithCredential, `token_id=""`) {
		t.Fatalf("the credentialed refusal logged no token id: %q", refusedWithCredential)
	}
	for _, secret := range []string{"not-the-secret", read, f.folder.ID} {
		if strings.Contains(logged, secret) {
			t.Fatalf("%q leaked into the log:\n%s", secret, logged)
		}
	}

	// A successful read is not logged: a shelf is a request per book, and
	// that is a reading history, not a diagnostic.
	if err := f.st.AssignUserFolder(t.Context(), f.user.ID, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if resp, _ := f.opds(t, "/opds/v1.2", "token", read); resp.StatusCode != http.StatusOK {
		t.Fatalf("root feed: %d", resp.StatusCode)
	}
	if strings.Contains(buf.String(), "opds request failed") {
		t.Fatalf("a successful catalog read was logged as a failure:\n%s", buf.String())
	}
}
