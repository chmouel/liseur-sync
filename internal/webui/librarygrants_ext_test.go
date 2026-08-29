//go:build linux

package webui_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestLibraryExplainsAnEmptyShelfIssue13. The bug reported in issue #13:
// an administrator added a folder, the scan found the book, and the
// Library page said the server watched no folders. It said that because
// the page tested the reader's granted folders and reported the answer
// as a fact about the server. The two states have different remedies, so
// the page has to tell them apart.
func TestLibraryExplainsAnEmptyShelfIssue13(t *testing.T) {
	f := newBooksFixture(t)
	f.addBook(t, "moby", []byte(strings.Repeat("web-epub", 50)))

	if err := f.st.UnassignUserFolder(t.Context(), "u1", f.folder); err != nil {
		t.Fatal(err)
	}
	_, body := f.get(t, "/ui/library", f.cookie)
	if strings.Contains(body, "watches no folders") {
		t.Fatal("a server with a folder told the reader it watches none")
	}
	if !strings.Contains(body, "none is assigned to your account") {
		t.Fatalf("the page did not name the real situation:\n%s", excerpt(body))
	}

	// The other state, and the one the sentence was written for.
	if err := f.st.DeleteFolder(t.Context(), f.folder); err != nil {
		t.Fatal(err)
	}
	if _, body := f.get(t, "/ui/library", f.cookie); !strings.Contains(
		body, "watches no folders",
	) {
		t.Fatalf("a server with no folders did not say so:\n%s", excerpt(body))
	}
}

// TestLibraryKeepsReadingHistoryWhenAGrantGoesAway. ADR-0027 says
// revoking a grant removes no reading state. Until this test the page
// computed those rows and then threw them away, because one branch
// around the whole shelf turned "no granted folder" into "render
// nothing". A reader could lose sight of years of reading because an
// administrator moved a shelf.
func TestLibraryKeepsReadingHistoryWhenAGrantGoesAway(t *testing.T) {
	f := newBooksFixture(t)
	bookID := f.addBook(t, "moby", []byte(strings.Repeat("web-epub", 50)))
	now := time.Now().UTC()

	work := store.Work{
		ID: "issue13-work", UserID: "u1",
		Title: "Moby Dick", Author: "Herman Melville", CreatedAt: now,
	}
	if _, err := f.st.ResolveCatalogBookWork(
		t.Context(), "u1", bookID, work, nil, nil, true, now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.AppendOps(t.Context(), "u1", "issue13-device", []store.Op{{
		OpID: "issue13-op", WorkID: work.ID, ClientTS: now,
		Progression: 0.42, Origin: store.OriginNative,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UnassignUserFolder(t.Context(), "u1", f.folder); err != nil {
		t.Fatal(err)
	}

	for _, filter := range []string{"all", "reading", "finished"} {
		resp, body := f.get(t, "/ui/library?filter="+filter, f.cookie)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("filter %s: status %d", filter, resp.StatusCode)
		}
		if filter == "finished" {
			// Nothing is finished, but the shelf still has to be a shelf
			// rather than a notice about folders.
			if strings.Contains(body, "watches no folders") {
				t.Fatalf("filter finished blamed the server:\n%s", excerpt(body))
			}
			continue
		}
		if !strings.Contains(body, "Moby Dick") {
			t.Fatalf("filter %s lost the reader's own work:\n%s", filter, excerpt(body))
		}
		if !strings.Contains(body, "Herman Melville") {
			t.Fatalf("filter %s lost the author:\n%s", filter, excerpt(body))
		}
		if !strings.Contains(body, "42") {
			t.Fatalf("filter %s lost the progress:\n%s", filter, excerpt(body))
		}
		// The work renders as the reader's private record of it, and
		// carries nothing from the catalog book it can no longer see.
		for _, leak := range []string{
			bookID, f.folder, "Alice's Books", f.root,
			"/ui/read/", "/v1/books/", "/ui/covers/",
		} {
			if strings.Contains(body, leak) {
				t.Fatalf("filter %s leaked %q from a hidden book:\n%s",
					filter, leak, excerpt(body))
			}
		}
		// ADR-0024 refuses to delete a work a catalog book still maps, so
		// offering the button would only produce an error.
		if strings.Contains(body, `name="work" value="`+work.ID+`"`) {
			t.Fatalf("filter %s offered a delete the store would refuse:\n%s",
				filter, excerpt(body))
		}
	}
}

func excerpt(body string) string {
	if len(body) > 3000 {
		return body[:3000]
	}
	return body
}
