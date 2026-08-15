package webui

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// The button that joins a library's books to its owner's shelf belongs
// on the page where the question is asked. Somebody who has just pointed
// the server at a folder is looking at the library they made, and the
// only control for this used to live on the Users page, under an
// unrelated account — which is how a filled library came to look empty
// to the one person who has run this.
func TestLibrariesPageJoinsBooksToTheOwnersShelf(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	root := "/srv/books"
	now := time.Now().UTC()
	if err := st.CreateLibrary(ctx, store.Library{
		ID: "lib-bob", OwnerUserID: bob.ID, QuotaUserID: bob.ID,
		Source:  store.LibraryDirectory,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshInterval, Name: "Bobs shelf",
		RootPath: &root, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/admin/libraries")
	if code != 200 {
		t.Fatalf("libraries page: %d", code)
	}
	// The control is named for whose shelf it fills, because an
	// administrator looking at somebody else's library is not filling
	// their own.
	if !strings.Contains(body, "Join these books to bob's shelf") {
		t.Fatalf("libraries page offers no way to join books to a shelf:\n%s", body)
	}
	csrf := extractCSRF(t, body)

	code, body = postForm(t, ts, cookie,
		"/ui/admin/libraries/lib-bob/backfill", url.Values{"csrf": {csrf}})
	if code != 200 {
		t.Fatalf("joining books to a shelf: %d", code)
	}
	// An empty catalog is still a run that answered, and it answers on
	// the Libraries page rather than bouncing to an account page.
	if !strings.Contains(body, "books to bob's shelf") {
		t.Fatalf("no report after joining books to a shelf:\n%s", body)
	}
	if !strings.Contains(body, "Bobs shelf") {
		t.Fatalf("joining books left the Libraries page:\n%s", body)
	}
}

// The mutation is a POST that writes a user's work graph, so it is
// refused without the session's CSRF token like every other one.
func TestJoinBooksToShelfRequiresCSRF(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	root := "/srv/books"
	now := time.Now().UTC()
	if err := st.CreateLibrary(ctx, store.Library{
		ID: "lib-bob", OwnerUserID: bob.ID, QuotaUserID: bob.ID,
		Source:  store.LibraryDirectory,
		Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshInterval, Name: "Bobs shelf",
		RootPath: &root, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	if code, _ := postForm(t, ts, cookie,
		"/ui/admin/libraries/lib-bob/backfill",
		url.Values{"csrf": {"wrong"}}); code != 403 {
		t.Fatalf("POST without a valid CSRF token: want 403, got %d", code)
	}
}
