package webui

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// mkDeletableLibrary makes one library of the given storage kind, owned
// by a second account, so the tests below also prove that an admin may
// remove a library they do not own.
func mkDeletableLibrary(
	t *testing.T, st store.Store, id string, storage store.LibraryStorage,
) store.User {
	t.Helper()
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	now := time.Now().UTC()
	lib := store.Library{
		ID: id, OwnerUserID: bob.ID, QuotaUserID: bob.ID,
		Source:  store.LibraryManaged,
		Storage: storage,
		Refresh: store.LibraryRefreshManual, Name: "Bobs shelf",
		CreatedAt: now, UpdatedAt: now,
	}
	if storage == store.LibraryStorageInPlace {
		root := "/srv/books"
		lib.Source, lib.RootPath = store.LibraryDirectory, &root
	}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	return bob
}

// A library kept in place is removed on one press: the folder it was
// reading belongs to somebody else and is untouched, so there is nothing
// to be careful with. The page says so, because "delete" over somebody's
// ebook collection is a frightening word if you are not told what it
// reaches.
func TestDeletingAnInPlaceLibraryRemovesItAtOnce(t *testing.T) {
	ts, st := testServer(t)
	mkDeletableLibrary(t, st, "lib-inplace", store.LibraryStorageInPlace)

	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/admin/libraries")
	if code != 200 {
		t.Fatalf("libraries page: %d", code)
	}
	for _, want := range []string{
		"Remove this library", "Remove Bobs shelf",
		"Not one file in that folder is touched",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("libraries page is missing %q:\n%s", want, body)
		}
	}
	csrf := extractCSRF(t, body)

	code, body = postForm(t, ts, cookie, "/ui/admin/libraries/lib-inplace/delete",
		url.Values{"csrf": {csrf}, "admin_password": {"hunter2hunter"}})
	if code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if !strings.Contains(body, "Bobs shelf is gone") {
		t.Fatalf("no confirmation that the library went:\n%s", body)
	}
	if !strings.Contains(body, "Nothing in /srv/books was touched") {
		t.Fatalf("the notice does not say the folder was left alone:\n%s", body)
	}
	if _, err := st.AdminLibraryByID(t.Context(), "lib-inplace"); err != store.ErrNotFound {
		t.Fatalf("library survived: %v", err)
	}
}

// A library holding the server's own copies takes two presses. The first
// says what it did and what to do next, because a message that only says
// "trashed" leaves somebody pressing a button that appears not to work.
func TestDeletingAnUploadsLibraryTrashesThenRemoves(t *testing.T) {
	ts, st := testServer(t)
	mkDeletableLibrary(t, st, "lib-cas", store.LibraryStorageCAS)

	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/admin/libraries")
	if code != 200 {
		t.Fatalf("libraries page: %d", code)
	}
	if !strings.Contains(body, "the only copy of the books in this library") {
		t.Fatalf("the page does not warn that the copies are the server's:\n%s",
			body)
	}
	csrf := extractCSRF(t, body)

	// Nothing is in it, so there is nothing to be careful with and one
	// press is enough. The careful path is covered in the store suite,
	// where books can be put in a library without an upload.
	code, body = postForm(t, ts, cookie, "/ui/admin/libraries/lib-cas/delete",
		url.Values{"csrf": {csrf}, "admin_password": {"hunter2hunter"}})
	if code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if !strings.Contains(body, "Bobs shelf is gone") {
		t.Fatalf("no confirmation that the library went:\n%s", body)
	}
	// The folder reassurance belongs only to a library that had one.
	if strings.Contains(body, "was touched") {
		t.Fatalf("an uploads library claims a folder was left alone:\n%s", body)
	}
}

// Removal is the one control on this page that can lose somebody's
// books, so a session left open is not enough on its own.
func TestDeletingALibraryCostsTheAdminsPassword(t *testing.T) {
	ts, st := testServer(t)
	mkDeletableLibrary(t, st, "lib-guard", store.LibraryStorageInPlace)

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/admin/libraries")
	csrf := extractCSRF(t, body)

	_, body = postForm(t, ts, cookie, "/ui/admin/libraries/lib-guard/delete",
		url.Values{"csrf": {csrf}, "admin_password": {"not-it"}})
	if !strings.Contains(body, "your password is wrong") {
		t.Fatalf("a wrong password deleted a library:\n%s", body)
	}
	if _, err := st.AdminLibraryByID(t.Context(), "lib-guard"); err != nil {
		t.Fatalf("library went despite the wrong password: %v", err)
	}

	if code, _ := postForm(t, ts, cookie, "/ui/admin/libraries/lib-guard/delete",
		url.Values{"csrf": {"wrong"}, "admin_password": {"hunter2hunter"}},
	); code != 403 {
		t.Fatalf("POST without a valid CSRF token: want 403, got %d", code)
	}
	if _, err := st.AdminLibraryByID(t.Context(), "lib-guard"); err != nil {
		t.Fatalf("library went without a CSRF token: %v", err)
	}
}
