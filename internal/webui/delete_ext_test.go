//go:build linux

package webui_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// missingBookFixture is the shelf the delete controls are about: one
// book still here and read, one book the folder no longer holds but the
// catalog still lists, and one work no catalog book backs at all.
//
// The missing book is made missing the way it happens in life — the
// file leaves the folder and a complete pass runs — because that is the
// only state the "is this a decision or a disk?" question has.
func missingBookFixture(t *testing.T) (*booksFixture, map[string]string) {
	t.Helper()
	f, ids := libraryFixture(t)
	now := time.Now().UTC()

	gone := f.addBook(t, "vanished", []byte(strings.Repeat("vanish", 60)))
	if _, err := f.st.ResolveCatalogBookWork(t.Context(), "u1", gone,
		store.Work{ID: "w-vanished", UserID: "u1", Title: "Vanished", CreatedAt: now},
		nil, nil, true, now); err != nil {
		t.Fatal(err)
	}
	progressOn(t, f, "w-vanished", "018e6f1a-0000-7000-8000-0000000000ee", 0.7, now)
	rmBook(t, f, "vanished.epub")
	ids["vanished"] = gone
	return f, ids
}

func rmBook(t *testing.T, f *booksFixture, relative string) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.root, relative)); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
}

// TestDeleteWorkForgetsAWorkNoBookBacks is the reader's own delete: a
// work that only ever came from a device goes, and takes the shelf tile
// with it.
func TestDeleteWorkForgetsAWorkNoBookBacks(t *testing.T) {
	f, _ := missingBookFixture(t)

	_, page := f.get(t, "/ui/library?folder="+f.folder+"&filter=all", f.cookie)
	if !strings.Contains(page, `action="works/w-elsewhere/delete"`) {
		t.Fatalf("a work no book backs has no delete on the shelf:\n%s", page)
	}
	csrf := csrfFrom(t, page)

	resp := f.postForm(t, "/ui/works/w-elsewhere/delete", f.cookie,
		url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}
	// The Location is resolved by the browser against the URL it posted
	// to, so it has to climb back out of /ui/works/{id}/delete.
	if got := resp.Header.Get("Location"); got != "../../library?notice=reading+history+deleted" {
		t.Fatalf("delete sent the browser to %q", got)
	}
	if _, err := f.st.WorkByID(t.Context(), "u1", "w-elsewhere"); err == nil {
		t.Fatal("the work survived its own delete")
	}
	_, page = f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	if strings.Contains(page, "works/w-elsewhere") {
		t.Fatalf("the deleted work is still on the shelf:\n%s", page)
	}
}

// TestDeleteWorkRefusesWhatTheCatalogStillHolds is the guard: a book
// this server lists keeps its reading, whether or not the last pass
// could find the file. An unplugged disk is not a decision.
func TestDeleteWorkRefusesWhatTheCatalogStillHolds(t *testing.T) {
	f, _ := missingBookFixture(t)

	_, page := f.get(t, "/ui/library?folder="+f.folder, f.cookie)
	csrf := csrfFrom(t, page)
	for _, workID := range []string{"w-midway", "w-vanished"} {
		if strings.Contains(page, `action="works/`+workID+`/delete"`) {
			t.Errorf("%s is in the catalog and was offered a delete:\n%s", workID, page)
		}
		resp := f.postForm(t, "/ui/works/"+workID+"/delete", f.cookie,
			url.Values{"csrf": {csrf}})
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("%s: status %d, want a redirect saying no", workID, resp.StatusCode)
		}
		if got := resp.Header.Get("Location"); !strings.HasPrefix(got, "../../library?problem=") {
			t.Errorf("%s: refused without saying why, or to the wrong page: %q", workID, got)
		}
		if _, err := f.st.WorkByID(t.Context(), "u1", workID); err != nil {
			t.Errorf("%s: a refused delete took the work anyway: %v", workID, err)
		}
	}

	// The work page agrees with the shelf: no delete section there
	// either, for a work the catalog still holds.
	_, page = f.get(t, "/ui/works/w-vanished", f.cookie)
	if strings.Contains(page, "w-vanished/delete") {
		t.Fatalf("the work page offered a delete the shelf refuses:\n%s", page)
	}
	_, page = f.get(t, "/ui/works/w-elsewhere", f.cookie)
	if !strings.Contains(page, "works/w-elsewhere/delete") {
		t.Fatalf("the work page has no delete for a work no book backs:\n%s", page)
	}
}

// TestDeleteWorkIsPerReader keeps the boundary every other piece of
// reading state keeps, and refuses a post with no token.
func TestDeleteWorkIsPerReader(t *testing.T) {
	f, _ := missingBookFixture(t)

	if resp := f.postForm(t, "/ui/works/w-elsewhere/delete", f.cookie,
		url.Values{"csrf": {"not-the-token"}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("delete without a CSRF token: status %d", resp.StatusCode)
	}

	bob := f.login(t, "bob")
	_, page := f.get(t, "/ui/library?folder="+f.folder, bob)
	resp := f.postForm(t, "/ui/works/w-elsewhere/delete", bob,
		url.Values{"csrf": {csrfFrom(t, page)}})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("one reader deleting another's work: status %d", resp.StatusCode)
	}
	if _, err := f.st.WorkByID(t.Context(), "u1", "w-elsewhere"); err != nil {
		t.Fatalf("another reader's post took the work: %v", err)
	}
}

// TestDeleteMissingBookIsAdminWork covers the shared half: retiring a
// catalog row is an administrator's decision, an active book is refused
// because a pass would put it back, and readers keep what they read.
func TestDeleteMissingBookIsAdminWork(t *testing.T) {
	f, ids := missingBookFixture(t)

	_, page := f.get(t, "/ui/books/"+ids["vanished"], f.cookie)
	csrf := csrfFrom(t, page)
	if strings.Contains(page, "/delete") {
		t.Fatalf("an ordinary reader was offered a catalog delete:\n%s", page)
	}
	if resp := f.postForm(t, "/ui/books/"+ids["vanished"]+"/delete", f.cookie,
		url.Values{"csrf": {csrf}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a non-admin deleting a catalog row: status %d", resp.StatusCode)
	}

	if err := f.st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	_, page = f.get(t, "/ui/books/"+ids["vanished"], f.cookie)
	if !strings.Contains(page, `action="../books/`+ids["vanished"]+`/delete"`) {
		t.Fatalf("an admin was not offered the delete for a missing book:\n%s", page)
	}
	csrf = csrfFrom(t, page)

	// An active book is not deletable: the next pass would add it back.
	if resp := f.postForm(t, "/ui/books/"+ids["midway"]+"/delete", f.cookie,
		url.Values{"csrf": {csrf}}); !strings.HasPrefix(
		resp.Header.Get("Location"), "../../library?folder="+f.folder+"&problem=") {
		t.Fatalf("deleting an active book was not refused: %v", resp.Header)
	}

	resp := f.postForm(t, "/ui/books/"+ids["vanished"]+"/delete", f.cookie,
		url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete of a missing book: status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "../../library?folder="+f.folder+
		"&notice=removed+from+the+catalog" {
		t.Fatalf("the catalog delete sent the browser to %q", got)
	}
	if _, err := f.st.CatalogBookByID(t.Context(), "", ids["vanished"]); err == nil {
		t.Fatal("the missing book is still in the catalog")
	}
	// The reader keeps the reading, now as a work they may delete.
	if _, err := f.st.WorkByID(t.Context(), "u1", "w-vanished"); err != nil {
		t.Fatalf("a reader's work went with the catalog row: %v", err)
	}
	_, page = f.get(t, "/ui/library?folder="+f.folder+"&filter=all", f.cookie)
	if !strings.Contains(page, `action="works/w-vanished/delete"`) {
		t.Fatalf("the work the delete orphaned has no delete of its own:\n%s", page)
	}
}

// TestDeleteMissingBookRefusedInACalibreFolder pins the other half of
// ADR-0022: there metadata.db is authoritative, so a book it still
// lists — unservable, and therefore marked missing — is put back by the
// next pass. A button that promised otherwise would be a lie, so there
// is none, and the route says no as well.
func TestDeleteMissingBookRefusedInACalibreFolder(t *testing.T) {
	f, _ := libraryFixture(t)
	if err := f.st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	calibre := store.Folder{
		ID: "folder-calibre", Name: "Calibre", RootPath: t.TempDir(),
		Kind: store.FolderCalibre, CreatedAt: time.Now().UTC(),
	}
	if err := f.st.CreateFolder(t.Context(), calibre); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AssignUserFolder(t.Context(), "u1", calibre.ID); err != nil {
		t.Fatal(err)
	}
	// Catalogued first, then observed as unservable — a format this
	// server cannot serve, which is how a Calibre book comes to be
	// marked missing while metadata.db still lists it.
	now := time.Now().UTC()
	id := int64(7)
	if _, err := f.st.ReconcileFolder(t.Context(), calibre.ID, []store.ObservedBook{{
		CalibreID: &id, RelativePath: "Author/Title (7)/title.epub",
		SizeBytes: 1, MTime: now, ContentSHA256: "sha-calibre", Title: "Converted",
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ReconcileFolder(t.Context(), calibre.ID, []store.ObservedBook{{
		CalibreID: &id, Unservable: true,
	}}, true, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// BooksInFolder rather than the listing: a missing book is kept but
	// not advertised, so the listing does not show it.
	books, err := f.st.BooksInFolder(t.Context(), calibre.ID)
	if err != nil || len(books) != 1 {
		t.Fatalf("want one catalogued book, got %v (%v)", books, err)
	}
	bookID := books[0].ID
	if books[0].Status != store.BookMissing {
		t.Fatalf("the unservable book is not missing: %+v", books[0])
	}

	_, page := f.get(t, "/ui/books/"+bookID, f.cookie)
	if strings.Contains(page, bookID+"/delete") {
		t.Fatalf("a Calibre book was offered a delete a pass would undo:\n%s", page)
	}
	resp := f.postForm(t, "/ui/books/"+bookID+"/delete", f.cookie,
		url.Values{"csrf": {csrfFrom(t, page)}})
	if got := resp.Header.Get("Location"); !strings.Contains(got, "problem=") {
		t.Fatalf("the route allowed a Calibre delete: %d %q", resp.StatusCode, got)
	}
	if _, err := f.st.CatalogBookByID(t.Context(), "", bookID); err != nil {
		t.Fatalf("the Calibre book was deleted anyway: %v", err)
	}
}
