package webui_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// bookWithMetadata promotes one book and applies a starting state to it,
// so the form is tested against metadata that came from somewhere rather
// than metadata the test typed into the form it is testing.
func bookWithMetadata(t *testing.T, f *booksFixture, name string) string {
	t.Helper()
	_, html := f.get(t, "/ui/books", f.cookie)
	f.uploadForm(t, f.cookie, csrfFrom(t, html), f.library, name+".epub",
		[]byte(strings.Repeat(name, 40)))
	return f.promote(t, name)
}

// saveMetadata submits the edit form the way its Save button does.
func saveMetadata(
	t *testing.T, f *booksFixture, cookie *http.Cookie, bookID string, form url.Values,
) *http.Response {
	t.Helper()
	return f.postForm(t, "/ui/books/"+bookID+"/metadata", cookie, form)
}

func TestBookPageEditSavesAndLocksAgainstRescan(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "edited")

	_, html := f.get(t, "/ui/books/"+bookID, f.cookie)
	csrf := csrfFrom(t, html)
	if !strings.Contains(html, "Correct this book") {
		t.Fatalf("librarian was not offered the edit form:\n%s", html)
	}

	resp := saveMetadata(t, f, f.cookie, bookID, url.Values{
		"csrf":             {csrf},
		"title":            {"A Corrected Title"},
		"title_was":        {""},
		"tags":             {"space, politics"},
		"tags_was":         {""},
		"series":           {"Dune #2"},
		"series_was":       {""},
		"contributors":     {"Frank Herbert (author)\nBrian Herbert (editor)"},
		"contributors_was": {""},
	})
	if resp.StatusCode != http.StatusSeeOther ||
		strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("save: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	md, err := f.st.CatalogBookMetadata(
		t.Context(), "u1", bookID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if md.Book.Title != "A Corrected Title" {
		t.Fatalf("title is %q", md.Book.Title)
	}
	// The lock is the point of the operation. Without it the next pass
	// over the file quietly undoes what the librarian just decided.
	if md.Book.TitleSource != store.MetadataManual || !md.Book.TitleLocked {
		t.Fatalf("title came back %s locked=%v", md.Book.TitleSource, md.Book.TitleLocked)
	}
	if len(md.Tags) != 2 || !md.Book.SetLocks.Tags {
		t.Fatalf("tags: %+v locked=%v", md.Tags, md.Book.SetLocks.Tags)
	}
	if len(md.Series) != 1 || md.Series[0].Name != "Dune" ||
		md.Series[0].Position == nil || *md.Series[0].Position != 2 {
		t.Fatalf("series did not round-trip: %+v", md.Series)
	}
	if len(md.Contributors) != 2 ||
		md.Contributors[0].Name != "Frank Herbert" ||
		md.Contributors[0].Role != "author" {
		t.Fatalf("contributors did not round-trip: %+v", md.Contributors)
	}
	// A role that is not the default must survive, because "who wrote it"
	// and "who edited it" are different questions about the same book.
	roles := map[string]string{}
	for _, c := range md.Contributors {
		roles[c.Name] = c.Role
	}
	if roles["Brian Herbert"] != "editor" {
		t.Fatalf("roles: %v", roles)
	}
}

// A librarian who opens a book, reads it and presses Save has asserted
// nothing. If that locked every field, looking at a book would stop it
// being catalogued — so an untouched form must be a no-op.
func TestBookPageSavingAnUntouchedFormChangesNothing(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "untouched")
	before, err := f.st.CatalogBookMetadata(
		t.Context(), "u1", bookID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}

	_, html := f.get(t, "/ui/books/"+bookID, f.cookie)
	csrf := csrfFrom(t, html)
	form := url.Values{"csrf": {csrf}}
	for _, field := range []string{
		"title", "subtitle", "description", "publisher", "published",
		"tags", "genres", "languages", "series", "contributors",
	} {
		value := formValueFrom(html, field)
		form.Set(field, value)
		form.Set(field+"_was", value)
	}
	resp := saveMetadata(t, f, f.cookie, bookID, form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", resp.StatusCode)
	}

	after, err := f.st.CatalogBookMetadata(
		t.Context(), "u1", bookID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if after.Book.Revision != before.Book.Revision {
		t.Fatalf("revision moved from %d to %d on an untouched form",
			before.Book.Revision, after.Book.Revision)
	}
	if after.Book.TitleLocked && !before.Book.TitleLocked {
		t.Fatal("an untouched form locked the title")
	}
	if after.Book.SetLocks.Tags && !before.Book.SetLocks.Tags {
		t.Fatal("an untouched form locked the tags")
	}
}

// formValueFrom reads back what the page rendered into a hidden `_was`
// field, which is the only honest way to submit an untouched form.
func formValueFrom(html, field string) string {
	marker := `name="` + field + `_was" value="`
	i := strings.Index(html, marker)
	if i < 0 {
		return ""
	}
	rest := html[i+len(marker):]
	return strings.ReplaceAll(rest[:strings.Index(rest, `"`)], "&#34;", `"`)
}

func TestBookPageEditIsClosedToReadersAndForgedForms(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "closed")
	_, html := f.get(t, "/ui/books/"+bookID, f.cookie)
	csrf := csrfFrom(t, html)

	reader := f.readerCookie(t)
	_, readerHTML := f.get(t, "/ui/books/"+bookID, reader)
	if strings.Contains(readerHTML, "Correct this book") {
		t.Fatalf("a reader was offered the edit form:\n%s", readerHTML)
	}
	// A reader must not learn where a value came from either: provenance
	// is librarian's information rather than a fact about the book.
	if strings.Contains(readerHTML, "edited by hand") ||
		strings.Contains(readerHTML, "from the file") {
		t.Fatalf("a reader was shown provenance:\n%s", readerHTML)
	}

	readerCSRF := csrfFrom(t, readerHTML)
	resp := saveMetadata(t, f, reader, bookID, url.Values{
		"csrf": {readerCSRF}, "title": {"Reader's Title"}, "title_was": {""},
	})
	if resp.StatusCode != http.StatusSeeOther ||
		!strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("a reader's edit was not refused: %d %s",
			resp.StatusCode, resp.Header.Get("Location"))
	}

	// The librarian's own token from another session must not work
	// without the CSRF field, or any page anywhere could rewrite a
	// library's catalogue.
	resp = saveMetadata(t, f, f.cookie, bookID, url.Values{
		"title": {"Forged"}, "title_was": {""},
	})
	if resp.StatusCode != http.StatusSeeOther ||
		!strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("a form with no csrf was accepted: %d", resp.StatusCode)
	}
	_ = csrf

	md, err := f.st.CatalogBookMetadata(
		t.Context(), "u1", bookID, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if md.Book.Title == "Reader's Title" || md.Book.Title == "Forged" {
		t.Fatalf("a refused edit was written anyway: %q", md.Book.Title)
	}
}

func TestEntityPagesListRenameAndMerge(t *testing.T) {
	f := newBooksFixture(t)
	one := bookWithMetadata(t, f, "alpha")
	two := bookWithMetadata(t, f, "beta")

	_, html := f.get(t, "/ui/books/"+one, f.cookie)
	csrf := csrfFrom(t, html)
	for bookID, author := range map[string]string{
		one: "Ursula K. Le Guin", two: "Le Guin, Ursula K.",
	} {
		resp := saveMetadata(t, f, f.cookie, bookID, url.Values{
			"csrf":             {csrf},
			"contributors":     {author},
			"contributors_was": {""},
		})
		if resp.StatusCode != http.StatusSeeOther ||
			strings.Contains(resp.Header.Get("Location"), "problem=") {
			t.Fatalf("seeding %s: %s", bookID, resp.Header.Get("Location"))
		}
	}

	_, html = f.get(t, "/ui/libraries/"+f.library+"/contributors", f.cookie)
	// Normalization folds case and spacing only, so two spellings this
	// far apart are two contributors until a person says otherwise. That
	// is exactly the state the merge tool exists for.
	if !strings.Contains(html, "Ursula K. Le Guin") ||
		!strings.Contains(html, "Le Guin, Ursula K.") {
		t.Fatalf("both spellings are not listed:\n%s", html)
	}
	if !strings.Contains(html, "Merge two") {
		t.Fatalf("librarian was not offered the merge tool:\n%s", html)
	}

	ids := entityIDs(t, f, store.EntityContributor)
	from, into := ids["Le Guin, Ursula K."], ids["Ursula K. Le Guin"]
	if from == "" || into == "" {
		t.Fatalf("could not find both contributors: %v", ids)
	}

	// Renaming onto a name already taken is refused with an offer to
	// merge, rather than quietly becoming a merge.
	resp := f.postForm(t,
		"/ui/libraries/"+f.library+"/contributors/"+from+"/rename", f.cookie,
		url.Values{"csrf": {csrf}, "name": {"Ursula K. Le Guin"}})
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "merge") {
		t.Fatalf("a colliding rename was not refused with a merge: %s", loc)
	}

	resp = f.postForm(t, "/ui/libraries/"+f.library+"/contributors/merge",
		f.cookie, url.Values{"csrf": {csrf}, "from": {from}, "into": {into}})
	if resp.StatusCode != http.StatusSeeOther ||
		strings.Contains(resp.Header.Get("Location"), "problem=") {
		t.Fatalf("merge: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	_, html = f.get(t, "/ui/libraries/"+f.library+"/contributors", f.cookie)
	if strings.Contains(html, "Le Guin, Ursula K.") {
		t.Fatalf("the merged-away spelling survived:\n%s", html)
	}
	_, html = f.get(t,
		"/ui/libraries/"+f.library+"/contributors/"+into, f.cookie)
	if !strings.Contains(html, "books/"+one) || !strings.Contains(html, "books/"+two) {
		t.Fatalf("the surviving contributor does not list both books:\n%s", html)
	}
}

func entityIDs(
	t *testing.T, f *booksFixture, kind store.EntityKind,
) map[string]string {
	t.Helper()
	rows, err := f.st.ListCatalogEntities(
		t.Context(), "u1", f.library, kind, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, row := range rows {
		out[row.Name] = row.ID
	}
	return out
}

func TestEntityPagesRefuseUnknownKindsAndReaderMutations(t *testing.T) {
	f := newBooksFixture(t)
	bookID := bookWithMetadata(t, f, "gated")
	_, html := f.get(t, "/ui/books/"+bookID, f.cookie)
	csrf := csrfFrom(t, html)
	if resp := saveMetadata(t, f, f.cookie, bookID, url.Values{
		"csrf": {csrf}, "tags": {"borrowed"}, "tags_was": {""},
	}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("seeding a tag: %d", resp.StatusCode)
	}

	// A kind outside the closed set names no resource. It must 404
	// rather than reach the store, where the kind selects a table.
	for _, kind := range []string{"books", "users", "libraries"} {
		resp, _ := f.get(t, "/ui/libraries/"+f.library+"/"+kind, f.cookie)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("kind %q answered %d, want 404", kind, resp.StatusCode)
		}
	}

	reader := f.readerCookie(t)
	resp, readerHTML := f.get(t, "/ui/libraries/"+f.library+"/tags", reader)
	if resp.StatusCode != http.StatusOK || !strings.Contains(readerHTML, "borrowed") {
		t.Fatalf("a reader cannot browse by tag: %d\n%s", resp.StatusCode, readerHTML)
	}
	if strings.Contains(readerHTML, "Merge two") ||
		strings.Contains(readerHTML, "Rename") {
		t.Fatalf("a reader was offered the librarian's tools:\n%s", readerHTML)
	}

	tagID := entityIDs(t, f, store.EntityTag)["borrowed"]
	readerCSRF := csrfFrom(t, readerHTML)
	mutation := f.postForm(t,
		"/ui/libraries/"+f.library+"/tags/"+tagID+"/rename", reader,
		url.Values{"csrf": {readerCSRF}, "name": {"stolen"}})
	if !strings.Contains(mutation.Header.Get("Location"), "problem=") {
		t.Fatalf("a reader renamed a tag: %s", mutation.Header.Get("Location"))
	}
	if got := entityIDs(t, f, store.EntityTag); got["stolen"] != "" {
		t.Fatalf("the rename went through anyway: %v", got)
	}
}

// A library the user cannot read must be invisible rather than empty:
// an empty page is still an answer about somebody else's library.
func TestEntityPagesAreScopedToTheTenant(t *testing.T) {
	f := newBooksFixture(t)
	if err := f.st.CreateLibrary(t.Context(), store.Library{
		ID: "lib-other", OwnerUserID: "u2", QuotaUserID: "u2",
		Kind: store.LibraryManaged, Name: "Bob's Books",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/ui/libraries/lib-other/tags",
		"/ui/libraries/lib-other/tags/whatever",
	} {
		if resp, _ := f.get(t, path, f.cookie); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s answered %d, want 404", path, resp.StatusCode)
		}
	}
}
