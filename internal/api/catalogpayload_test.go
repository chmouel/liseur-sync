package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// credit attaches contributors and series to a book the way an editor
// would, so a payload test reads a book that is actually related to
// something.
func (f *uploadFixture) credit(
	t *testing.T, bookID string,
	contributors []store.BookContributor, series []store.BookSeries,
) {
	t.Helper()
	metadata, err := f.st.CatalogBookMetadata(
		t.Context(), f.user.ID, bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Contributors = contributors
	metadata.Series = series
	if _, err := f.st.ApplyCatalogBookMetadata(t.Context(), f.user.ID,
		store.ApplyBookMetadataRequest{
			Metadata:         metadata,
			ExpectedRevision: metadata.Book.Revision,
			UpdatedAt:        time.Now().UTC(),
		}); err != nil {
		t.Fatal(err)
	}
}

// TestCatalogBookShapeIsIdenticalOnEveryRoute is the whole point of
// ADR-0015: there is one representation of a catalog book, and a client
// parses it once. A route that returned a thinner row would send the
// client back for the same data one book at a time.
func TestCatalogBookShapeIsIdenticalOnEveryRoute(t *testing.T) {
	f := newUploadFixture(t)
	body := []byte("the bytes of a related book")
	bookID, digest := f.publishAs(t, "shape", "Neuromancer", body)
	position := 1.0
	f.credit(t, bookID,
		[]store.BookContributor{
			{ContributorID: "c-gibson", Name: "William Gibson",
				NormalizedName: "william gibson", Role: store.ContributorRoleAuthor,
				Position: 1, Source: store.MetadataEmbedded},
			{ContributorID: "c-bell", Name: "Anthea Bell",
				NormalizedName: "anthea bell", Role: "translator",
				Position: 2, Source: store.MetadataEmbedded},
		},
		[]store.BookSeries{
			{SeriesID: "s-sprawl", Name: "Sprawl", NormalizedName: "sprawl",
				Position: &position, Source: store.MetadataEmbedded},
			{SeriesID: "s-omnibus", Name: "Omnibus", NormalizedName: "omnibus",
				Source: store.MetadataEmbedded},
		})
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	first := func(t *testing.T, path string) map[string]any {
		t.Helper()
		code, page := getJSON(t, f.ts.URL+path, read)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %v", path, code, page)
		}
		books, _ := page["books"].([]any)
		if len(books) != 1 {
			t.Fatalf("%s: books = %v", path, page["books"])
		}
		row, _ := books[0].(map[string]any)
		return row
	}

	listed := first(t, "/v1/libraries/"+f.library+"/books")
	searched := first(t, f.searchPath(url.Values{"q": {"neuromancer"}}))
	byEntity := first(t,
		"/v1/libraries/"+f.library+"/entities/contributors/c-gibson/books")
	// A twin with the same bytes gives the duplicate report a group to
	// return. It is published after the listings above so those still see
	// exactly one book.
	f.publishAs(t, "shape-twin", "Neuromancer Again", body)
	duplicated := f.duplicateRow(t, bookID, read)
	code, detail := getJSON(t, f.ts.URL+"/v1/books/"+bookID, read)
	if code != http.StatusOK {
		t.Fatalf("detail: %d %v", code, detail)
	}
	for name, got := range map[string]map[string]any{
		"search": searched, "entity listing": byEntity,
		"duplicate group": duplicated, "detail": detail,
	} {
		if !reflect.DeepEqual(got, listed) {
			t.Errorf("%s payload differs from the listing:\n%v\n%v",
				name, got, listed)
		}
	}

	// A catalog-only credential sees exactly what a credential holding
	// every scope sees: nothing in this payload is reading state, so
	// there is nothing for a wider token to add (ADR-0006).
	everything := f.mintScopes(t, f.user.ID, "wide",
		store.ScopeLibraryRead, store.ScopeLibraryManage, store.ScopeSync)
	code, wide := getJSON(t, f.ts.URL+"/v1/books/"+bookID, everything)
	if code != http.StatusOK {
		t.Fatalf("detail with every scope: %d %v", code, wide)
	}
	if !reflect.DeepEqual(wide, listed) {
		t.Errorf("a wider token saw a different book:\n%v\n%v", wide, listed)
	}

	// Every contributor in every role, in the order the book stores them:
	// which of them a shelf shows is the client's decision.
	people, _ := listed["contributors"].([]any)
	if len(people) != 2 {
		t.Fatalf("contributors = %v", listed["contributors"])
	}
	author, _ := people[0].(map[string]any)
	if author["name"] != "William Gibson" || author["role"] != "author" ||
		author["id"] != "c-gibson" {
		t.Errorf("first contributor = %v", author)
	}
	if translator, _ := people[1].(map[string]any); translator["role"] != "translator" {
		t.Errorf("second contributor = %v", people[1])
	}

	// Every series, because the catalog allows several and picking one
	// would be the server deciding which shelf the book belongs on.
	rows, _ := listed["series"].([]any)
	if len(rows) != 2 {
		t.Fatalf("series = %v", listed["series"])
	}
	placed := map[string]any{}
	for _, row := range rows {
		entry, _ := row.(map[string]any)
		placed[entry["name"].(string)] = entry["position"]
	}
	if placed["Sprawl"] != 1.0 {
		t.Errorf("series position = %v", placed["Sprawl"])
	}
	// Omitted rather than zero: a book nobody placed in a series is not
	// the first book in it.
	if position, present := placed["Omnibus"]; present && position != nil {
		t.Errorf("an unknown series position was invented: %v", position)
	}

	files, _ := listed["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v", listed["files"])
	}
	file, _ := files[0].(map[string]any)
	if file["sha256"] != digest {
		t.Errorf("file digest = %v, want the content digest %s", file["sha256"], digest)
	}
	if file["size_bytes"] != float64(len(body)) {
		t.Errorf("size_bytes = %v, want %d", file["size_bytes"], len(body))
	}
	if file["media_type"] != "application/epub+zip" || file["filename"] != "shape.epub" {
		t.Errorf("file = %v", file)
	}
}

// A book nobody wrote, shelved in no series, with nothing to download is
// still a book. The relationship fields are present and empty, because an
// absent field and an empty one are different bugs to a client and only
// one of them is true.
func TestCatalogPayloadKeepsEmptyRelationsPresent(t *testing.T) {
	f := newUploadFixture(t)
	at := time.Now().UTC()
	if err := f.st.CreateCatalogBook(t.Context(), f.user.ID, store.CatalogBook{
		ID: "book-bare", LibraryID: f.library, Status: store.BookActive,
		Title: "Nothing attached", CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)

	code, detail := getJSON(t, f.ts.URL+"/v1/books/book-bare", read)
	if code != http.StatusOK {
		t.Fatalf("detail: %d %v", code, detail)
	}
	for _, field := range []string{"contributors", "series", "files"} {
		rows, present := detail[field]
		if !present {
			t.Fatalf("%s is missing from a book that has none", field)
		}
		if list, _ := rows.([]any); len(list) != 0 {
			t.Fatalf("%s = %v, want empty", field, rows)
		}
	}
}

// countingStore reports how many batched relation reads a request made.
// A page of books is a bounded number of rows; if enriching it were a
// lookup per row, the N+1 this ADR removed from the client would have
// moved into the server rather than gone away.
type countingStore struct {
	store.Store
	calls int
}

func (c *countingStore) CatalogBookRelationsForBooks(
	ctx context.Context, userID string, bookIDs []string,
) (store.CatalogBookRelations, error) {
	c.calls++
	return c.Store.CatalogBookRelationsForBooks(ctx, userID, bookIDs)
}

func TestCatalogListingReadsRelationsOnceRegardlessOfPageSize(t *testing.T) {
	f := newUploadFixture(t)
	for _, name := range []string{"one", "two", "three", "four"} {
		bookID, _ := f.publish(t, name, []byte("bytes of "+name))
		f.credit(t, bookID, []store.BookContributor{
			{ContributorID: "c-" + name, Name: "Author " + name,
				NormalizedName: "author " + name, Role: store.ContributorRoleAuthor,
				Position: 1, Source: store.MetadataEmbedded},
		}, nil)
	}
	read := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	counting := &countingStore{Store: f.st}
	f.srv.St = counting

	code, page := getJSON(t, f.ts.URL+"/v1/libraries/"+f.library+"/books", read)
	if code != http.StatusOK {
		t.Fatalf("books: %d %v", code, page)
	}
	if books, _ := page["books"].([]any); len(books) != 4 {
		t.Fatalf("books = %v", page["books"])
	}
	if counting.calls != 1 {
		t.Fatalf("a four-book page made %d relation reads, want 1", counting.calls)
	}
}

// duplicateRow digs the row for one book out of the duplicate report.
func (f *uploadFixture) duplicateRow(
	t *testing.T, bookID, token string,
) map[string]any {
	t.Helper()
	code, page := getJSON(t,
		f.ts.URL+"/v1/libraries/"+f.library+"/duplicates", token)
	if code != http.StatusOK {
		t.Fatalf("duplicates: %d %v", code, page)
	}
	groups, _ := page["duplicates"].([]any)
	for _, entry := range groups {
		group, _ := entry.(map[string]any)
		books, _ := group["books"].([]any)
		for _, row := range books {
			book, _ := row.(map[string]any)
			if book["book_id"] == bookID {
				return book
			}
		}
	}
	t.Fatalf("no duplicate group holds %s: %v", bookID, page["duplicates"])
	return nil
}

// A trashed book offers nothing to download, because it cannot be
// downloaded. Echoing the files it will get back on restore would invite
// a client to try, and a deleted book is deleted as far as every reader
// is concerned — so its relations go quiet too.
func TestTrashedBookOffersNoFiles(t *testing.T) {
	f := newUploadFixture(t)
	bookID, _ := f.publishAs(t, "doomed", "Doomed", []byte("bytes to delete"))
	f.credit(t, bookID, []store.BookContributor{
		{ContributorID: "c-doomed", Name: "Some Author",
			NormalizedName: "some author", Role: store.ContributorRoleAuthor,
			Position: 1, Source: store.MetadataEmbedded},
	}, nil)

	resp, raw := f.req(t, http.MethodDelete, "/v1/books/"+bookID, f.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d %s", resp.StatusCode, raw)
	}
	var deleted map[string]any
	if err := json.Unmarshal(raw, &deleted); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"files", "contributors", "series"} {
		rows, present := deleted[field]
		if !present {
			t.Fatalf("%s is missing from the delete response", field)
		}
		if list, _ := rows.([]any); len(list) != 0 {
			t.Fatalf("a trashed book still advertises %s: %v", field, rows)
		}
	}
	if resp, _ := f.get(t, "/v1/books/"+bookID+"/download", f.token); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("download of a trashed book = %d, want 404", resp.StatusCode)
	}

	// Restoring puts the book back together, files and credits included.
	resp, raw = f.req(t, http.MethodPost, "/v1/books/"+bookID+"/restore", f.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore: %d %s", resp.StatusCode, raw)
	}
	var restored map[string]any
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if files, _ := restored["files"].([]any); len(files) != 1 {
		t.Fatalf("a restored book has no file: %v", restored["files"])
	}
	if people, _ := restored["contributors"].([]any); len(people) != 1 {
		t.Fatalf("a restored book lost its author: %v", restored["contributors"])
	}
}
