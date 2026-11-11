package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// parsed2Feed is the loose reader-side view of an OPDS 2.0 feed: tests
// read the JSON the way a client does rather than through the server's
// own structs.
type parsed2Feed struct {
	Metadata struct {
		Title        string `json:"title"`
		ItemsPerPage int    `json:"itemsPerPage"`
	} `json:"metadata"`
	Links        []parsed2Link `json:"links"`
	Navigation   []parsed2Link `json:"navigation"`
	Publications []parsed2Pub  `json:"publications"`
	Groups       []struct {
		Metadata struct {
			Title string `json:"title"`
		} `json:"metadata"`
		Links        []parsed2Link `json:"links"`
		Publications []parsed2Pub  `json:"publications"`
	} `json:"groups"`
}

type parsed2Link struct {
	Href       string `json:"href"`
	Type       string `json:"type"`
	Rel        string `json:"rel"`
	Title      string `json:"title"`
	Templated  bool   `json:"templated"`
	Properties struct {
		NumberOfItems int `json:"numberOfItems"`
	} `json:"properties"`
}

type parsed2Pub struct {
	Metadata struct {
		Type       string `json:"@type"`
		Identifier string `json:"identifier"`
		Title      string `json:"title"`
		Author     []struct {
			Name  string        `json:"name"`
			Links []parsed2Link `json:"links"`
		} `json:"author"`
	} `json:"metadata"`
	Links  []parsed2Link `json:"links"`
	Images []parsed2Link `json:"images"`
}

func (f *folderFixture) opds2(t *testing.T, path, token string) parsed2Feed {
	t.Helper()
	resp, raw := f.opds(t, path, "token", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", path, resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); ct != opds2Type {
		t.Fatalf("%s content-type = %q", path, ct)
	}
	var feed parsed2Feed
	if err := json.Unmarshal(raw, &feed); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, raw)
	}
	return feed
}

func link2(links []parsed2Link, rel, typ string) string {
	for _, l := range links {
		if l.Rel == rel && (typ == "" || l.Type == typ) {
			return l.Href
		}
	}
	return ""
}

func TestOPDS2WalksFromRootToBytes(t *testing.T) {
	f := newFolderFixture(t)
	body := makeEPUB(t, "Feed Walked Book", "Ann Author", bytes.Repeat([]byte("opds2"), 64))
	f.writeBook(t, "walk.epub", body)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	root := f.opds2(t, "/opds/v2", read)
	var folderHref string
	for _, l := range root.Navigation {
		if l.Title == "Books" {
			folderHref = l.Href
		}
	}
	if folderHref == "" {
		t.Fatalf("no folder in navigation: %+v", root.Navigation)
	}
	if len(root.Groups) != 1 || len(root.Groups[0].Publications) != 1 ||
		link2(root.Groups[0].Links, "self", "") != "/opds/v2/new" {
		t.Fatalf("root has no Newest group: %+v", root.Groups)
	}

	folder := f.opds2(t, folderHref, read)
	if folder.Metadata.Title != "Books" || len(folder.Publications) != 1 {
		t.Fatalf("folder feed = %+v", folder)
	}
	pub := folder.Publications[0]
	if pub.Metadata.Type != opds2BookType || pub.Metadata.Title != "Feed Walked Book" ||
		len(pub.Metadata.Author) != 1 || pub.Metadata.Author[0].Name != "Ann Author" {
		t.Fatalf("publication metadata = %+v", pub.Metadata)
	}
	if len(pub.Images) == 0 || len(pub.Links) != 1 {
		t.Fatalf("publication links = %+v images = %+v", pub.Links, pub.Images)
	}

	// The author links to the books that author wrote.
	byAuthor := f.opds2(t, pub.Metadata.Author[0].Links[0].Href, read)
	if len(byAuthor.Publications) != 1 {
		t.Fatalf("author feed = %+v", byAuthor)
	}

	resp, got := f.opds(t, link2(pub.Links, opdsAcquisitionRel, "application/epub+zip"), "token", read)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
		t.Fatalf("acquisition: %d, %d bytes", resp.StatusCode, len(got))
	}
}

// TestOPDS2RefusesWithAnAuthenticationDocument: an OPDS 2.0 client
// learns how to sign in from the 401 itself, and a Basic-only client
// still gets the challenge header it prompts on.
func TestOPDS2RefusesWithAnAuthenticationDocument(t *testing.T) {
	f := newFolderFixture(t)
	for _, tc := range []struct{ name, user, pass string }{
		{"no credential", "", ""},
		{"account password", f.user.Name, "hunter2hunter"},
		{"wrong secret", "token", "not-a-token"},
	} {
		resp, raw := f.opds(t, "/opds/v2", tc.user, tc.pass)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: %d %s", tc.name, resp.StatusCode, raw)
		}
		if ct := resp.Header.Get("Content-Type"); ct != opds2AuthDocType {
			t.Fatalf("%s: content-type %q", tc.name, ct)
		}
		if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic ") {
			t.Fatalf("%s: challenge %q", tc.name, got)
		}
		var doc struct {
			ID             string `json:"id"`
			Title          string `json:"title"`
			Authentication []struct {
				Type   string            `json:"type"`
				Labels map[string]string `json:"labels"`
			} `json:"authentication"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v %s", tc.name, err, raw)
		}
		if doc.ID == "" || doc.Title == "" || len(doc.Authentication) != 1 ||
			doc.Authentication[0].Type != opds2BasicAuth ||
			doc.Authentication[0].Labels["login"] == "" {
			t.Fatalf("%s: auth document = %s", tc.name, raw)
		}
	}

	sync := f.mintToken(t, f.user.ID, store.ScopeSync)
	resp, raw := f.opds(t, "/opds/v2", "token", sync)
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("sync token: %d %q %s", resp.StatusCode, resp.Header.Get("WWW-Authenticate"), raw)
	}
}

// TestOPDS2OffersAudiobooksOnlyWithBothGrants: the audiobook link and
// its bytes follow the native audio route's rule, a grant on the book's
// folder and on the audiobook's.
func TestOPDS2OffersAudiobooksOnlyWithBothGrants(t *testing.T) {
	f := newFolderFixture(t)
	ctx := t.Context()
	ch := `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><body>
<p>The morning sun rose slowly over the silent mountains and warmed the valley stones.</p>
</body></html>`
	epubBody := buildTestAudioEPUB(t, "Shadows Upon Time", "Christopher Ruocchio", ch, ch)
	if err := os.WriteFile(filepath.Join(f.root, "shadows.epub"), epubBody, 0o644); err != nil {
		t.Fatal(err)
	}
	audioRoot := t.TempDir()
	m4b := buildSliceableTestM4B("Shadows Upon Time", "Christopher Ruocchio")
	if err := os.WriteFile(filepath.Join(audioRoot, "shadows.m4b"), m4b, 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	audio := store.Folder{ID: store.NewID(), Name: "Audio", RootPath: audioRoot,
		Kind: store.FolderPlain, CreatedAt: now, UpdatedAt: now}
	if err := f.st.CreateFolder(ctx, audio); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AssignUserFolder(ctx, f.user.ID, audio.ID); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	// Not reconcileFolder, which would grant the audio folder to both.
	if _, err := f.rec.Reconcile(ctx, audio); err != nil {
		t.Fatal(err)
	}

	mine := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	theirs := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)

	pubs := f.opds2(t, "/opds/v2/new", mine).Publications
	if len(pubs) != 1 {
		t.Fatalf("publications = %+v", pubs)
	}
	audioHref := link2(pubs[0].Links, opdsAcquisitionRel, "audio/mp4")
	if audioHref == "" {
		t.Fatalf("no audiobook link: %+v", pubs[0].Links)
	}
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+audioHref, nil)
	req.SetBasicAuth("token", mine)
	req.Header.Set("Range", "bytes=0-9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), `filename="shadows.m4b"`) {
		t.Fatalf("audiobook: %d %q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}

	pubs = f.opds2(t, "/opds/v2/new", theirs).Publications
	if len(pubs) != 1 || link2(pubs[0].Links, opdsAcquisitionRel, "audio/mp4") != "" {
		t.Fatalf("audiobook offered without its folder grant: %+v", pubs)
	}
	if resp, raw := f.opds(t, audioHref, "token", theirs); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ungranted audiobook: %d %s", resp.StatusCode, raw)
	}
}

func TestOPDS2PagesNewWithoutLosingBooks(t *testing.T) {
	f := newFolderFixture(t)
	const total = opdsPageSize + 3
	want := map[string]bool{}
	for i := range total {
		id, _ := f.publish(t, fmt.Sprintf("p%02d", i), []byte(fmt.Sprintf("body-%d", i)))
		want["/opds/v2/books/"+id+"/download"] = false
	}
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	path, pages := "/opds/v2/new", 0
	for path != "" {
		if pages++; pages > 10 {
			t.Fatal("feed never ends")
		}
		feed := f.opds2(t, path, read)
		for _, p := range feed.Publications {
			href := link2(p.Links, opdsAcquisitionRel, "")
			if seen, known := want[href]; !known || seen {
				t.Fatalf("unexpected or repeated %q", href)
			}
			want[href] = true
		}
		path = link2(feed.Links, "next", "")
	}
	if pages != 2 {
		t.Fatalf("paged %d times, want 2", pages)
	}
	for href, seen := range want {
		if !seen {
			t.Fatalf("%s never appeared", href)
		}
	}
}

func TestOPDS2SearchesThroughItsTemplate(t *testing.T) {
	f := newFolderFixture(t)
	f.publishAs(t, "a", "Lighthouse Keeper", []byte("a"))
	f.publishAs(t, "b", "Unrelated", []byte("b"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	folder := f.opds2(t, "/opds/v2/folders/"+f.folder.ID, read)
	var tmpl string
	for _, l := range folder.Links {
		if l.Rel == "search" && l.Templated {
			tmpl = l.Href
		}
	}
	if !strings.HasSuffix(tmpl, "{?query}") {
		t.Fatalf("no templated search link: %+v", folder.Links)
	}
	results := f.opds2(t, strings.TrimSuffix(tmpl, "{?query}")+"?query="+url.QueryEscape("lighthouse"), read)
	if len(results.Publications) != 1 || results.Publications[0].Metadata.Title != "Lighthouse Keeper" {
		t.Fatalf("results = %+v", results.Publications)
	}
}

func TestOPDS2EscapesHostileMetadata(t *testing.T) {
	f := newFolderFixture(t)
	const evil = `</title><script>alert("xss")</script>& "quoted"`
	f.publishAs(t, "hostile", evil, []byte("body"))
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	resp, raw := f.opds(t, "/opds/v2/folders/"+f.folder.ID, "token", read)
	if resp.StatusCode != http.StatusOK || bytes.Contains(raw, []byte("<script>")) {
		t.Fatalf("feed: %d\n%s", resp.StatusCode, raw)
	}
	var feed parsed2Feed
	if err := json.Unmarshal(raw, &feed); err != nil || len(feed.Publications) != 1 ||
		feed.Publications[0].Metadata.Title != evil {
		t.Fatalf("title did not round-trip: %v %s", err, raw)
	}
}

func TestOPDS2RejectsUnknownPathsAndBadCursors(t *testing.T) {
	f := newFolderFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for _, path := range []string{"/opds/v2", "/opds/v2/"} {
		f.opds2(t, path, read)
	}
	for _, path := range []string{"/opds/v2/nonsense", "/opds/v2/folders", "/opds/v2/shelves/nope"} {
		if resp, _ := f.opds(t, path, "token", read); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, resp.StatusCode)
		}
	}
	for _, path := range []string{
		"/opds/v2?cursor=e30", "/opds/v2/new?cursor=!!!",
		"/opds/v2/folders/" + f.folder.ID + "?cursor=e30",
	} {
		if resp, _ := f.opds(t, path, "token", read); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", path, resp.StatusCode)
		}
	}
}

// TestOPDS2EmptyFeedsKeepTheirCollection: a feed with nothing in it
// still names its collection as an empty array. The schema asks for at
// least one item, which an empty page cannot have; an empty array is
// what clients handle, where a missing key reads as a malformed feed.
func TestOPDS2EmptyFeedsKeepTheirCollection(t *testing.T) {
	f := newFolderFixture(t)
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	for path, key := range map[string]string{
		"/opds/v2/new":                    `"publications":[]`,
		"/opds/v2/shelves/reading":        `"publications":[]`,
		"/opds/v2/entities/series":        `"navigation":[]`,
		"/opds/v2/folders/" + f.folder.ID: `"publications":[]`,
	} {
		resp, raw := f.opds(t, path, "token", read)
		if resp.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(key)) {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, raw)
		}
	}
}

// TestOPDS12PointsAtItsOPDS2Twin: a 1.2 feed advertises the JSON feed
// with the same content, so a client that speaks both can switch.
func TestOPDS12PointsAtItsOPDS2Twin(t *testing.T) {
	f := newFolderFixture(t)
	for i := range opdsPageSize + 1 {
		f.publish(t, fmt.Sprintf("t%02d", i), []byte(fmt.Sprintf("twin-%d", i)))
	}
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	for path, want := range map[string]string{
		"/opds/v1.2": "/opds/v2",
		"/opds/v1.2/folders/" + f.folder.ID + "/search?q=twin": "/opds/v2/folders/" + f.folder.ID +
			"/search?query=twin",
		"/opds/v1.2/shelves/unread": "/opds/v2/shelves/unread",
	} {
		resp, raw := f.opds(t, path, "token", read)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		got := linkHref(t, parseFeed(t, raw).Links, "alternate")
		if got != want {
			t.Fatalf("%s alternate = %q, want %q", path, got, want)
		}
		f.opds2(t, got, read)
	}

	// The root's later pages continue in /new, at the same cursor.
	resp, raw := f.opds(t, "/opds/v1.2", "token", read)
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode)
	}
	next := linkHref(t, parseFeed(t, raw).Links, "next")
	_, raw = f.opds(t, next, "token", read)
	alt := linkHref(t, parseFeed(t, raw).Links, "alternate")
	if !strings.HasPrefix(alt, "/opds/v2/new?cursor=") {
		t.Fatalf("second page alternate = %q", alt)
	}
	if got := f.opds2(t, alt, read).Publications; len(got) != 1 {
		t.Fatalf("second page in 2.0 = %d books, want 1", len(got))
	}
}

func TestOPDS2Identifier(t *testing.T) {
	for _, tc := range []struct {
		ids  []store.BookIdentifier
		want string
	}{
		{[]store.BookIdentifier{{Scheme: "isbn", Value: "978-0-14-044913-6"}}, "urn:isbn:9780140449136"},
		{[]store.BookIdentifier{{Scheme: "isbn", Value: "urn:isbn:014044913x"}}, "urn:isbn:014044913X"},
		{[]store.BookIdentifier{{Scheme: "uuid", Value: "urn:uuid:0D0A6B5C-1111-2222-3333-444455556666"}},
			"urn:uuid:0d0a6b5c-1111-2222-3333-444455556666"},
		{[]store.BookIdentifier{{Scheme: "isbn", Value: "not an isbn <x>"}}, "urn:liseur:book:b1"},
		{nil, "urn:liseur:book:b1"},
	} {
		if got := opds2Identifier("b1", tc.ids); got != tc.want {
			t.Errorf("%+v = %q, want %q", tc.ids, got, tc.want)
		}
	}
}
