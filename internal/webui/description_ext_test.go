//go:build linux

package webui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestBookPageRendersADescriptionAsMarkup is the regression test for
// descriptions arriving as HTML: Calibre wraps every blurb in
// <div align="justify">, and the page used to print those tags at the
// reader instead of rendering them.
//
// The description is now whatever the file said it was — nobody edits it
// here — which makes the sanitizing the only thing standing between a
// folder's contents and the page, and so worth more than it was.
func TestBookPageRendersADescriptionAsMarkup(t *testing.T) {
	f := newBooksFixture(t)
	desc := `<div align="justify">Il y a cinq cent mille ans, une ` +
		`supernova.</div><div align="justify">Voir ` +
		`<a href="http://www.amazon.fr/dp/B00X" onclick="steal()">le lien` +
		`</a>.</div><script>alert(1)</script>` +
		`<img src="http://tracker.example/p.gif" onerror="alert(2)">`
	bookID := f.observe(t, store.ObservedBook{
		RelativePath: "described.epub", SizeBytes: 4096, MTime: time.Now().UTC(),
		ContentSHA256:    strings.Repeat("d", 64),
		OriginalFilename: "described.epub", MediaType: "application/epub+zip",
		Title: "Described", Description: desc,
	})

	_, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	about := page[strings.Index(page, "<h2>About</h2>"):]
	about = about[:strings.Index(about, "</section>")]

	if strings.Contains(about, "&lt;div") {
		t.Errorf("the About card printed escaped markup:\n%s", about)
	}
	if !strings.Contains(about, "<div>Il y a cinq cent mille ans, une supernova.</div>") {
		t.Errorf("the description did not render as markup:\n%s", about)
	}
	if !strings.Contains(about, `<a href="http://www.amazon.fr/dp/B00X" `+
		`rel="nofollow noopener noreferrer" target="_blank">le lien</a>`) {
		t.Errorf("the link did not survive sanitizing:\n%s", about)
	}
	for _, bad := range []string{
		"alert(1)", "alert(2)", "<script", "onclick", "onerror", "<img",
		"tracker.example", "align=",
	} {
		if strings.Contains(about, bad) {
			t.Errorf("hostile markup %q reached the page:\n%s", bad, about)
		}
	}
	if strings.Contains(page, "alert(1)") &&
		!strings.Contains(page, "&lt;script&gt;alert(1)") {
		t.Errorf("a script from the description reached the response unescaped")
	}
	// There is nothing to correct it with: the folder is the source of
	// this text, and the server does not write to the folder.
	if strings.Contains(page, "<textarea") {
		t.Errorf("the book page still offers a metadata editor:\n%s", page)
	}
}
