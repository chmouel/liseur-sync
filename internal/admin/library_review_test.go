package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// reviewFixture puts one book into review the way a watched sweep would,
// so the commands are exercised against the state they exist to report on
// rather than against a state invented for the test.
func reviewFixture(t *testing.T, st store.Store) (libraryID, bookID string) {
	t.Helper()
	owner := addUser(t, st, "ada")
	out, err := capture(t, st, "create-library", "ada", "watched")
	if err != nil {
		t.Fatal(err)
	}
	libraryID = libraryIDFrom(t, out)
	bookID = "book-changed"
	now := time.Now().UTC()
	if err := st.CreateCatalogBook(t.Context(), owner.ID, store.CatalogBook{
		ID: bookID, LibraryID: libraryID, Status: store.BookActive,
		Title: "Ancillary Justice", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := st.SetCatalogBookReview(
		t.Context(), libraryID, bookID, "source-content-changed", now)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("the book did not enter review")
	}
	return libraryID, bookID
}

func TestListReviewNamesTheBookAndWhy(t *testing.T) {
	st := newAdminStore(t)
	libraryID, bookID := reviewFixture(t, st)

	out, err := capture(t, st, "list-review", "ada", libraryID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, bookID) {
		t.Fatalf("review listing did not name the book:\n%s", out)
	}
	// The reason is the whole point: "this book needs attention" without
	// saying why leaves the operator no better off than the log line.
	if !strings.Contains(out, "source-content-changed") {
		t.Fatalf("review listing did not say why:\n%s", out)
	}
	if !strings.Contains(out, "Ancillary Justice") {
		t.Fatalf("review listing did not show the title:\n%s", out)
	}
}

func TestListReviewIsScopedToTheActor(t *testing.T) {
	st := newAdminStore(t)
	libraryID, _ := reviewFixture(t, st)
	addUser(t, st, "bob")

	if _, err := capture(t, st, "list-review", "bob", libraryID); err == nil {
		t.Fatal("a user with no grant read another library's review queue")
	}
}

func TestClearReviewReturnsTheBookToMissingNotActive(t *testing.T) {
	st := newAdminStore(t)
	libraryID, bookID := reviewFixture(t, st)

	if _, err := capture(t, st, "clear-review", "ada", libraryID, bookID); err != nil {
		t.Fatal(err)
	}
	book, err := st.CatalogBookByID(t.Context(), "ada-id", bookID, store.LibraryRoleRead)
	if err != nil {
		t.Fatal(err)
	}
	// Only the availability pass may call a book servable. Clearing a
	// review here and declaring it active would be a second writer with
	// its own opinion about the same fact.
	if book.Status != store.BookMissing {
		t.Fatalf("cleared review left status %q, want missing", book.Status)
	}
	if book.ReviewReason != "" {
		t.Fatalf("cleared review kept the reason %q", book.ReviewReason)
	}

	out, err := capture(t, st, "list-review", "ada", libraryID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, bookID) {
		t.Fatalf("cleared book is still in the queue:\n%s", out)
	}
}

func TestClearReviewOnABookThatIsNotInReviewSaysSo(t *testing.T) {
	st := newAdminStore(t)
	libraryID, bookID := reviewFixture(t, st)
	if _, err := capture(t, st, "clear-review", "ada", libraryID, bookID); err != nil {
		t.Fatal(err)
	}

	out, err := capture(t, st, "clear-review", "ada", libraryID, bookID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not awaiting review") {
		t.Fatalf("a second clear reported success:\n%s", out)
	}
}

func TestReviewCommandsRejectBadInput(t *testing.T) {
	st := newAdminStore(t)
	libraryID, bookID := reviewFixture(t, st)

	for _, args := range [][]string{
		{"list-review"},
		{"list-review", "ada"},
		{"list-review", "ada", libraryID, "extra"},
		{"list-review", "nobody", libraryID},
		{"clear-review", "ada", libraryID},
		{"clear-review", "nobody", libraryID, bookID},
	} {
		if _, err := capture(t, st, args...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
