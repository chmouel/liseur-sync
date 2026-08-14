//go:build linux

package webui_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestBookPageRendersADescriptionAsMarkup is the regression test for
// descriptions arriving as HTML: Calibre wraps every blurb in
// <div align="justify">, and the page used to print those tags at the
// reader instead of rendering them.
func TestBookPageRendersADescriptionAsMarkup(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "described")

	_, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	desc := `<div align="justify">Il y a cinq cent mille ans, une ` +
		`supernova.</div><div align="justify">Voir ` +
		`<a href="http://www.amazon.fr/dp/B00X" onclick="steal()">le lien` +
		`</a>.</div><script>alert(1)</script>` +
		`<img src="http://tracker.example/p.gif" onerror="alert(2)">`
	resp := saveMetadata(t, f, f.cookie, bookID, url.Values{
		"csrf":            {csrfFrom(t, page)},
		"description":     {desc},
		"description_was": {""},
	})
	if resp.StatusCode != http.StatusSeeOther ||
		strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("save: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	_, page = f.get(t, "/ui/books/"+bookID, f.cookie)
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

	// The edit form is the one place that must still show the source
	// text, escaped, or a librarian could not correct it.
	if !strings.Contains(page, "&lt;div align=&#34;justify&#34;") &&
		!strings.Contains(page, "&lt;div align=&quot;justify&quot;") {
		t.Errorf("the edit textarea lost the raw description:\n%s", page)
	}
}
