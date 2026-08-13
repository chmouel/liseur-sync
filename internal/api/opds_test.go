//go:build linux

package api

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
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

func (f *uploadFixture) opds(t *testing.T, path, user, pass string) (*http.Response, []byte) {
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
	f := newUploadFixture(t)
	body := bytes.Repeat([]byte("opds-epub"), 64)
	_, digest := f.publish(t, "opds", body)
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
	libHref := linkHref(t, root.Entries[0].Links, "subsection")
	if libHref == "" {
		t.Fatalf("no subsection link: %+v", root.Entries[0])
	}

	resp, raw = f.opds(t, libHref, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("library feed: %d %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "kind=acquisition") {
		t.Fatalf("library content-type = %q", ct)
	}
	lib := parseFeed(t, raw)
	if len(lib.Entries) != 1 {
		t.Fatalf("library entries = %+v", lib.Entries)
	}
	entry := lib.Entries[0]
	if entry.Title != "Title of opds" {
		t.Fatalf("entry title = %q", entry.Title)
	}
	if entry.Publisher != "A Publisher" {
		t.Fatalf("entry publisher = %q", entry.Publisher)
	}
	acq := linkHref(t, entry.Links, opdsAcquisitionRel)
	if acq == "" {
		t.Fatalf("no acquisition link: %+v", entry)
	}

	resp, got := f.opds(t, acq, "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("acquisition: %d %s", resp.StatusCode, got)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(body))
	}
	if want := `"` + digest + `"`; resp.Header.Get("ETag") != want {
		t.Fatalf("etag = %q want %q", resp.Header.Get("ETag"), want)
	}
}

// TestOPDSRejectsAccountPasswords pins the rule that the catalog password
// is a token secret and never the account password. A reader stores its
// credential in plain text on the device; handing it the account password
// would put the whole account on an e-reader's filesystem.
func TestOPDSRejectsAccountPasswords(t *testing.T) {
	f := newUploadFixture(t)
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
	f := newUploadFixture(t)
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
	f := newUploadFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	name := "device-" + string(store.ScopeLibraryRead) + "-" + f.user.ID

	for _, tc := range []struct {
		user string
		want int
	}{
		{"token", http.StatusOK},
		{name, http.StatusOK},
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
	f := newUploadFixture(t)
	sync := f.mintToken(t, f.user.ID, store.ScopeSync)

	resp, raw := f.opds(t, "/opds/v1.2", "token", sync)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("sync token reached the catalog: %d %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != "" {
		t.Fatalf("challenged a valid credential: %q", got)
	}
}

// TestOPDSIsScopedToTheCallersLibraries: a stranger with a perfectly good
// catalog token sees an empty catalog, and cannot reach a library by
// guessing its id.
func TestOPDSIsScopedToTheCallersLibraries(t *testing.T) {
	f := newUploadFixture(t)
	f.publish(t, "private", []byte("secret"))
	stranger := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)

	resp, raw := f.opds(t, "/opds/v1.2", "token", stranger)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root: %d %s", resp.StatusCode, raw)
	}
	if entries := parseFeed(t, raw).Entries; len(entries) != 0 {
		t.Fatalf("stranger sees %d libraries", len(entries))
	}

	resp, raw = f.opds(t, "/opds/v1.2/libraries/"+f.library, "token", stranger)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("guessed library: %d %s", resp.StatusCode, raw)
	}
}

// TestOPDSPagesWithoutLosingBooks walks the `next` links exactly as a
// reader does and checks every book appears once. A feed that silently
// drops books is worse than one that fails.
func TestOPDSPagesWithoutLosingBooks(t *testing.T) {
	f := newUploadFixture(t)
	const total = defaultCatalogPageSize + 3
	want := map[string]bool{}
	for i := range total {
		id, _ := f.publish(t, fmt.Sprintf("p%02d", i), []byte(fmt.Sprintf("body-%d", i)))
		want["urn:liseur:book:"+id] = false
	}
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	path := "/opds/v1.2/libraries/" + f.library
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

// TestOPDSEscapesHostileMetadata: titles come out of uploaded EPUBs, so
// they are attacker-controlled. Many readers render feed text as HTML.
// The encoder must escape it, and the feed must still parse.
func TestOPDSEscapesHostileMetadata(t *testing.T) {
	f := newUploadFixture(t)
	const evil = `</title><script>alert("xss")</script>& "quoted" 'single'`
	f.publishAs(t, "hostile", evil, []byte("body"))

	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	resp, raw := f.opds(t, "/opds/v1.2/libraries/"+f.library, "token", read)
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
	f := newUploadFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, path := range []string{"/opds/v1.2/nonsense", "/opds/v1.2/libraries"} {
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
	f := newUploadFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, cursor := range []string{"!!!", "eyJ0Ijoibm90LWEtdGltZSJ9", "e30"} {
		resp, _ := f.opds(t,
			"/opds/v1.2/libraries/"+f.library+"?cursor="+cursor, "token", read)
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
	f := newUploadFixture(t)
	bookID, _ := f.publish(t, "opds-cover", coverEPUB(t, 300, 400, "cover.png"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	resp, raw := f.opds(t, "/opds/v1.2/libraries/"+f.library, "token", read)
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
