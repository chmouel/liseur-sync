//go:build linux

package webui_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The book page's Library organization card (ADR-0018). It used to be a
// heading and a button, so the only way to learn which series a book was
// in was to open the editor, and a reader whose own claim sat on top of
// what the folder said could not see that it did.

// organizationCard is the series part of that card, cut out so an
// assertion about it cannot be satisfied by the chips in the hero.
func organizationCard(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, `id="series-assign"`)
	if start < 0 {
		t.Fatalf("no series section on the book page:\n%s", page)
	}
	card := page[start:]
	if end := strings.Index(card, "<h2>File</h2>"); end >= 0 {
		card = card[:end]
	}
	return card
}

func TestBookPageNamesTheSeriesWithoutOpeningTheEditor(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "first", "Foundation", 1)

	resp, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("book page: %d", resp.StatusCode)
	}
	card := organizationCard(t, page)
	if !strings.Contains(card, "Foundation") {
		t.Errorf("the card does not name the series:\n%s", card)
	}
	if !strings.Contains(card, "#1") {
		t.Errorf("the card does not say where the book sits:\n%s", card)
	}
	if !strings.Contains(card, "This is what the folder says.") {
		t.Errorf("the card does not say which layer it is reading:\n%s", card)
	}
	// The folder is the layer in force, so repeating it underneath
	// would say the same thing twice.
	if strings.Contains(card, "The folder says:") {
		t.Errorf("the card quotes the folder against itself:\n%s", card)
	}
	// The editor is still a click away rather than rendered into every
	// book page.
	if !strings.Contains(card, "Change this book's series") {
		t.Errorf("the card no longer offers the editor:\n%s", card)
	}
}

func TestBookPageSaysWhatTheFolderSaidUnderneathAClaim(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "first", "Foundation", 1)

	_, form := f.getFragment(t, "/ui/books/"+bookID+"/series", f.cookie)
	resp, _ := f.postFragment(t, "/ui/books/"+bookID+"/series", f.cookie,
		url.Values{
			"csrf": {csrfFrom(t, form)},
			"name": {"Second Foundation"}, "position": {""},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claiming a series: %d", resp.StatusCode)
	}

	_, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	card := organizationCard(t, page)
	if !strings.Contains(card, "Second Foundation") {
		t.Errorf("the card does not name the claimed series:\n%s", card)
	}
	if !strings.Contains(card, "You arranged this. Only you see it.") {
		t.Errorf("the card does not say whose arrangement this is:\n%s", card)
	}
	if !strings.Contains(card, "The folder says: Foundation #1.") {
		t.Errorf("the card does not say what the claim covers up:\n%s", card)
	}

	// A claim is one reader's. Another account sees the folder's answer
	// and is told nothing about the claim or that one exists.
	bob := f.login(t, "bob")
	_, theirs := f.get(t, "/ui/books/"+bookID, bob)
	theirCard := organizationCard(t, theirs)
	if strings.Contains(theirCard, "Second Foundation") {
		t.Errorf("one reader's claim is on another reader's book page:\n%s", theirCard)
	}
	if !strings.Contains(theirCard, "This is what the folder says.") {
		t.Errorf("the folder's answer is not what a stranger sees:\n%s", theirCard)
	}
}

func TestBookPageSaysWhenABookIsInNoSeries(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "loner", []byte(strings.Repeat("web-epub", 50)))

	_, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	card := organizationCard(t, page)
	if !strings.Contains(card, "This book is in no series.") {
		t.Errorf("the card says nothing about a book in no series:\n%s", card)
	}
}

// TestSeriesDialogFieldsAreNamed covers the pair of text boxes the
// editor is made of. The placeholders disappear as soon as either has
// anything in it, and "#" does not say what number it wants, so the
// columns are headed and the fields carry a name of their own.
func TestSeriesDialogFieldsAreNamed(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "first", "Foundation", 1)

	resp, form := f.getFragment(t, "/ui/books/"+bookID+"/series", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dialog: %d", resp.StatusCode)
	}
	for _, want := range []string{
		`aria-label="Series name"`,
		`aria-label="Number in the series"`,
		"Book no.",
	} {
		if !strings.Contains(form, want) {
			t.Errorf("the dialog is missing %q:\n%s", want, form)
		}
	}
}

// TestSeriesResetWorksWithoutScripting is the guard on splitting the two
// buttons apart. Save and undo submit different forms, and putting them
// on one line means the undo button sits inside the form it does not
// belong to and names its own. That is only correct if a browser with no
// scripting still resets from it.
func TestSeriesResetWorksWithoutScripting(t *testing.T) {
	f := newBooksFixture(t)
	bookID := seriesBook(t, f, "first", "Foundation", 1)

	resp, page := f.get(t, "/ui/books/"+bookID+"/series", f.cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("following the link: %d", resp.StatusCode)
	}
	csrf := csrfFrom(t, page)
	if resp := f.postForm(t, "/ui/books/"+bookID+"/series", f.cookie,
		url.Values{
			"csrf": {csrf},
			"name": {"Second Foundation"}, "position": {""},
		}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("claiming without scripting: %d", resp.StatusCode)
	}

	_, withClaim := f.get(t, "/ui/books/"+bookID+"/series", f.cookie)
	if !strings.Contains(withClaim, `form="series-reset-form"`) {
		t.Errorf("the undo button does not name the form it submits:\n%s", withClaim)
	}
	if !strings.Contains(withClaim, `id="series-reset-form"`) {
		t.Errorf("the form the undo button names is not on the page:\n%s", withClaim)
	}

	if resp := f.postForm(t, "/ui/books/"+bookID+"/series/reset", f.cookie,
		url.Values{"csrf": {csrfFrom(t, withClaim)}},
	); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("undoing without scripting: %d", resp.StatusCode)
	}

	_, back := f.get(t, "/ui/books/"+bookID, f.cookie)
	card := organizationCard(t, back)
	if strings.Contains(card, "Second Foundation") {
		t.Errorf("the claim survived the undo:\n%s", card)
	}
	if !strings.Contains(card, "This is what the folder says.") {
		t.Errorf("the book did not go back to the folder's answer:\n%s", card)
	}
}
