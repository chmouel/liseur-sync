//go:build linux

package api

import (
	"net/http"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Merging and splitting a series over the API (ADR-0021). The store
// suite owns what a merge moves and what survives a scan; these cover
// the HTTP edge: that both are admin-only, that a merge answers with the
// survivor, that the absorbed name is listed as a binding a client can
// undo, and that a split nobody can perform is a 4xx rather than a 500.

// adminFixtureToken is a token on a real admin account: the scope alone
// is refused by the store unless the account carries the flag.
func adminFixtureToken(t *testing.T, f *folderFixture) string {
	t.Helper()
	boss := f.createUser(t, "merge-boss")
	if err := f.st.SetUserAdmin(t.Context(), boss.ID, true); err != nil {
		t.Fatal(err)
	}
	return f.mintToken(t, boss.ID, store.ScopeLibraryRead,
		store.ScopeLibraryManage, store.ScopeAdmin)
}

// claimSharedSeries puts a book on a shelf everybody can see, which is
// the only kind of shelf a merge speaks about.
func claimSharedSeries(t *testing.T, f *folderFixture, token, bookID, name string) {
	t.Helper()
	status, body := f.sendJSON(t, http.MethodPut, "/v1/books/"+bookID+"/series",
		token, map[string]any{"scope": "shared",
			"series": []map[string]any{{"name": name}}})
	if status != http.StatusOK {
		t.Fatalf("shared claim %q: %d (%v)", name, status, body)
	}
}

func TestSeriesMergeNeedsAdmin(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "metro", "Metro 2033", []byte("metro"))
	other, _ := f.publishAs(t, "metro2", "Metro 2034", []byte("metro2"))
	mine := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	claimSeries(t, f, mine, bookID, "Metro")
	claimSeries(t, f, mine, other, "Metro 2033")
	absorbed := seriesEntityID(t, f, mine, "Metro 2033")
	survivor := seriesEntityID(t, f, mine, "Metro")

	status, _ := f.sendJSON(t, http.MethodPost,
		"/v1/entities/series/"+absorbed+"/merge", mine,
		map[string]any{"into": survivor})
	if status != http.StatusForbidden {
		t.Fatalf("merge without admin: got %d, want 403", status)
	}
	status, _ = f.sendJSON(t, http.MethodGet,
		"/v1/entities/series/"+survivor+"/bindings", mine, nil)
	if status != http.StatusForbidden {
		t.Fatalf("listing bindings without admin: got %d, want 403", status)
	}
}

func TestSeriesMergeAnswersWithTheSurvivor(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "metro", "Metro 2033", []byte("metro"))
	other, _ := f.publishAs(t, "metro2", "Metro 2034", []byte("metro2"))
	admin := adminFixtureToken(t, f)
	claimSharedSeries(t, f, admin, bookID, "Metro")
	claimSharedSeries(t, f, admin, other, "Metro 2033")
	absorbed := seriesEntityID(t, f, admin, "Metro 2033")
	survivor := seriesEntityID(t, f, admin, "Metro")

	status, body := f.sendJSON(t, http.MethodPost,
		"/v1/entities/series/"+absorbed+"/merge", admin,
		map[string]any{"into": survivor})
	if status != http.StatusOK {
		t.Fatalf("merging: got %d, want 200 (%v)", status, body)
	}
	if body["id"] != survivor || body["name"] != "Metro" {
		t.Fatalf("merge answered with %v, want the survivor %q", body, survivor)
	}
	if count, ok := body["book_count"].(float64); !ok || count != 2 {
		t.Fatalf("survivor holds %v books, want 2", body["book_count"])
	}
	// The absorbed row is genuinely gone; nothing has to filter it out.
	status, _ = f.sendJSON(t, http.MethodGet,
		"/v1/entities/series/"+absorbed+"/bindings", admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("absorbed series still readable: %d", status)
	}

	status, body = f.sendJSON(t, http.MethodGet,
		"/v1/entities/series/"+survivor+"/bindings", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("listing bindings: %d", status)
	}
	bindings := body["bindings"].([]any)
	if len(bindings) != 1 {
		t.Fatalf("survivor absorbed %d names, want 1 (%v)", len(bindings), body)
	}
	row := bindings[0].(map[string]any)
	if row["name"] != "Metro 2033" {
		t.Fatalf("binding names %v, want the absorbed name", row["name"])
	}
	if row["folder_id"] != nil {
		t.Fatalf("a merge binds everywhere, got folder %v", row["folder_id"])
	}

	status, _ = f.sendJSON(t, http.MethodDelete,
		"/v1/entities/series/"+survivor+"/bindings/"+row["binding_id"].(string),
		admin, nil)
	if status != http.StatusNoContent {
		t.Fatalf("unbinding: got %d, want 204", status)
	}
}

func TestSeriesMergeRefusalsAreFourHundreds(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "metro", "Metro 2033", []byte("metro"))
	admin := adminFixtureToken(t, f)
	claimSharedSeries(t, f, admin, bookID, "Metro")
	id := seriesEntityID(t, f, admin, "Metro")

	for _, tc := range []struct {
		name string
		path string
		body map[string]any
		want int
	}{
		{"merge into itself", "/merge", map[string]any{"into": id},
			http.StatusBadRequest},
		{"merge into nothing", "/merge", map[string]any{"into": ""},
			http.StatusBadRequest},
		{"merge into a stranger", "/merge",
			map[string]any{"into": store.NewID()}, http.StatusNotFound},
		{"split without a folder", "/split", map[string]any{"name": "Metro A"},
			http.StatusBadRequest},
		// A shelf that exists only as a claim was never folded out of
		// an observed name, so there is nothing in that folder to
		// split off. Filing those books is what a claim is for.
		{"split a claimed shelf", "/split",
			map[string]any{"folder_id": f.folder.ID, "name": "Metro A"},
			http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := f.sendJSON(t, http.MethodPost,
				"/v1/entities/series/"+id+tc.path, admin, tc.body)
			if status != tc.want {
				t.Fatalf("got %d, want %d (%v)", status, tc.want, body)
			}
			if body["error"] == nil {
				t.Fatalf("no error message in %v", body)
			}
		})
	}
}
