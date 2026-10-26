//go:build linux

package webui_test

// A book's audiobook outside the reader (ADR-0058): the mark in the
// library, the player on the details page, and the form that adds one.
// The transfer itself is the API's and is tested there.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/audiomap"
	"github.com/chmouel/liseur-sync/internal/store"
)

// seedAudiobook pairs bookID with an audiobook in a second folder only
// alice holds, without an .m4b on disk: the folder is never scanned, so
// nothing marks it missing. mapped decides whether the link lines up.
func seedAudiobook(t *testing.T, f *booksFixture, bookID string, mapped bool) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	root := filepath.Join(t.TempDir(), "audio")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.st.CreateFolder(ctx, store.Folder{
		ID: "folder-audio", Name: "Audio", RootPath: root,
		Kind: store.FolderPlain, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AssignUserFolder(ctx, "u1", "folder-audio"); err != nil {
		t.Fatal(err)
	}
	chapters, _ := json.Marshal([]map[string]any{
		{"title": "Track 01", "start": 0, "end": 30},
		{"title": "Closing", "start": 30, "end": 60},
	})
	if _, err := f.st.ReconcileFolderAudiobooks(ctx, "folder-audio", []store.ObservedAudiobook{{
		RelativePath: "moby.m4b", SizeBytes: 10, MTime: now,
		ContentSHA256: "m4b-moby", Title: "Moby Audio", DurationSeconds: 60,
		NarratorsJSON: []byte(`["Nell Narrator"]`), ChaptersJSON: chapters,
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	known, err := f.st.AudiobooksInFolder(ctx, "folder-audio")
	if err != nil || len(known) != 1 {
		t.Fatalf("AudiobooksInFolder: %v %+v", err, known)
	}
	book, err := f.st.CatalogBookByID(ctx, "", bookID)
	if err != nil {
		t.Fatal(err)
	}
	ab, err := f.st.CatalogAudiobookByID(ctx, "", known[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	link := store.BookAudiobook{
		BookID: bookID, AudiobookID: ab.ID,
		EditionSHA: book.ContentSHA256, AudiobookSHA: ab.ContentSHA256,
		Source: "filename", State: store.BookAudiobookUnmapped,
		MappingKey: audiomap.CatalogMappingKey(book, ab),
	}
	if mapped {
		link.State = store.BookAudiobookActive
		link.MappingJSON, _ = json.Marshal(audiomap.Mapping{
			Duration: 60,
			Segments: []audiomap.Segment{{Label: "Track 01", Title: "Loomings", AudioEnd: 60, BookEnd: 1}},
		})
	}
	if err := f.st.UpsertBookAudiobook(ctx, link, now); err != nil {
		t.Fatal(err)
	}
}

func TestBookPageOffersAudiobookFormOnlyWhereAllowed(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "moby", []byte(strings.Repeat("web-epub", 50)))
	page := "/ui/books/" + bookID

	if _, body := f.get(t, page, f.cookie); strings.Contains(body, "data-audiobook-upload") {
		t.Fatal("a folder that never asked for uploads offered the audiobook form")
	}
	allowUploads(t, f)
	if _, body := f.get(t, page, f.cookie); !strings.Contains(body, "data-audiobook-upload") {
		t.Fatal("a folder that accepts uploads did not offer the audiobook form")
	}
	seedAudiobook(t, f, bookID, true)
	if _, body := f.get(t, page, f.cookie); strings.Contains(body, "data-audiobook-upload") {
		t.Fatal("a book that already has an audiobook offered the form")
	}
}

func TestPlayableAudiobookShowsInLibraryAndOnItsPage(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "moby", []byte(strings.Repeat("web-epub", 50)))
	silentID := f.addBook(t, "quiet", []byte(strings.Repeat("quiet-epub", 50)))
	seedAudiobook(t, f, bookID, true)

	_, body := f.get(t, "/ui/books/"+bookID, f.cookie)
	for _, want := range []string{
		`id="listen"`, `data-book-audio="` + bookID + `"`, "Nell Narrator", "Closing", "listen-btn",
		// A numbered track is named after the book chapter it was paired with.
		"0:00 · Loomings",
		`class="reader-audio-bar reader-audio-inline"`, `books/` + bookID + `/audio"></audio>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("details page lacks %q", want)
		}
	}
	// The reader's player, without what needs the text on screen.
	for _, reader := range []string{`id="reader-audio-follow"`, `id="reader-audio-to-audio"`, "data-audio-dock", "data-audio-handle"} {
		if strings.Contains(body, reader) {
			t.Errorf("details page player has the reader-only %q", reader)
		}
	}
	if _, body := f.get(t, "/ui/books/"+silentID, f.cookie); strings.Contains(body, `id="listen"`) {
		t.Error("a book with no audiobook has a listen section")
	}

	if _, body := f.get(t, "/ui/library", f.cookie); !strings.Contains(body, "audio-badge") {
		t.Error("library shows no audiobook badge")
	}
	_, body = f.get(t, "/ui/library?filter=audio", f.cookie)
	if !strings.Contains(body, "books/"+bookID) || strings.Contains(body, "books/"+silentID) {
		t.Error("the audiobooks filter did not list exactly the book with one")
	}

	// bob reads the same book but holds no grant on the audiobook's folder.
	bob := f.login(t, "bob")
	if _, body := f.get(t, "/ui/library", bob); strings.Contains(body, "audio-badge") {
		t.Error("an audiobook in an ungranted folder was badged")
	}
	if _, body := f.get(t, "/ui/books/"+bookID, bob); strings.Contains(body, `id="listen"`) {
		t.Error("an audiobook in an ungranted folder reached the details page")
	}
}

func TestUnmappedAudiobookIsNamedButNotPlayed(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "moby", []byte(strings.Repeat("web-epub", 50)))
	seedAudiobook(t, f, bookID, false)

	_, body := f.get(t, "/ui/books/"+bookID, f.cookie)
	if !strings.Contains(body, "could not be") || strings.Contains(body, "<audio") {
		t.Error("an unmapped audiobook should be explained, not played")
	}
	if _, body := f.get(t, "/ui/library", f.cookie); strings.Contains(body, "audio-badge") {
		t.Error("an unmapped audiobook was badged as playable")
	}
}

func TestAudiobookUploadRefusesAPostWithoutTheFormToken(t *testing.T) {
	f := newBooksFixture(t)
	allowUploads(t, f)
	bookID := f.addBook(t, "moby", []byte(strings.Repeat("web-epub", 50)))

	req, _ := http.NewRequest(http.MethodPost,
		f.ts.URL+"/ui/books/"+bookID+"/audiobook?csrf=forged", strings.NewReader("x"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	req.AddCookie(f.cookie)
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(loc, "problem=") {
		t.Fatalf("forged post: %d %q", resp.StatusCode, loc)
	}
	if back, err := req.URL.Parse(loc); err != nil || back.Path != "/ui/books/"+bookID {
		t.Fatalf("redirect %q does not lead back to the book's page", loc)
	}
	if entries, _ := os.ReadDir(f.root); len(entries) != 1 {
		t.Fatalf("a forged post wrote into the folder: %v", entries)
	}
}

func TestBookAudioMapsAudioTimeToTheBook(t *testing.T) {
	runNodeTests(t, "bookaudio.test.mjs")
}

// The audiobooks shelf pages over playable books only, so a pile has to
// stand where its playable volume is, not where its newest one is.
func TestAudiobooksShelfKeepsASeriesWithOnePlayableVolume(t *testing.T) {
	f := newBooksFixture(t)
	now := time.Now().UTC()
	one := observeSeriesBook(t, f, "one.epub", "Volume One", "The Run", 1, now)
	_ = observeSeriesBook(t, f, "two.epub", "Volume Two", "The Run", 2, now.Add(time.Second))
	seedAudiobook(t, f, one, true)

	_, page := f.get(t, "/ui/library?folder="+f.folder+"&filter=audio", f.cookie)
	if body := grid(page); strings.Count(body, `class="bookcard seriescard"`) != 1 {
		t.Fatalf("the audiobooks shelf lost the series with a playable volume:\n%s", body)
	}
}
