//go:build linux

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// Series renames over the API (ADR-0020). The store suite owns the
// layering; these cover the HTTP edge: which layer a caller may write,
// that a rename does not travel between readers, and that a name already
// taken is a 409 rather than a silent merge.

// seriesEntityID finds the series a fixture's book was catalogued into.
func seriesEntityID(t *testing.T, f *folderFixture, token, name string) string {
	t.Helper()
	status, body := getJSON(t, f.ts.URL+"/v1/entities/series", token)
	if status != http.StatusOK {
		t.Fatalf("listing series: %d", status)
	}
	for _, e := range body["entities"].([]any) {
		row := e.(map[string]any)
		if row["name"] == name {
			return row["id"].(string)
		}
	}
	t.Fatalf("no series named %q in %v", name, body)
	return ""
}

// claimSeries puts a book in a named series, which is how these tests
// get a series to rename without a folder that has one.
func claimSeries(t *testing.T, f *folderFixture, token, bookID, name string) {
	t.Helper()
	status, _ := f.sendJSON(t, http.MethodPut, "/v1/books/"+bookID+"/series",
		token, map[string]any{"series": []map[string]any{{"name": name}}})
	if status != http.StatusOK {
		t.Fatalf("claiming %q: %d", name, status)
	}
}

func TestSeriesRenameIsPrivateToItsAuthor(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "dune", "Dune", []byte("dune"))
	mine := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	theirs := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)
	claimSeries(t, f, mine, bookID, "Dune Chronicles")
	id := seriesEntityID(t, f, mine, "Dune Chronicles")

	status, body := f.sendJSON(t, http.MethodPut,
		"/v1/entities/series/"+id+"/name", mine,
		map[string]any{"name": "Chroniques de Dune"})
	if status != http.StatusOK {
		t.Fatalf("renaming: %d", status)
	}
	if body["name"] != "Chroniques de Dune" ||
		body["scanned_name"] != "Dune Chronicles" ||
		body["name_source"] != "personal" {
		t.Fatalf("the answer does not describe the rename: %v", body)
	}

	// A stranger sees the name the library gave it. They cannot see the
	// series at all here, because the claim that created it is personal
	// too, so the check is that the rename does not reach them.
	status, listing := getJSON(t, f.ts.URL+"/v1/entities/series", theirs)
	if status != http.StatusOK {
		t.Fatalf("stranger listing series: %d", status)
	}
	for _, e := range listing["entities"].([]any) {
		if e.(map[string]any)["name"] == "Chroniques de Dune" {
			t.Errorf("a personal rename leaked to another reader: %v", e)
		}
	}

	status, body = f.sendJSON(t, http.MethodDelete,
		"/v1/entities/series/"+id+"/name", mine, nil)
	if status != http.StatusOK {
		t.Fatalf("reverting: %d", status)
	}
	if body["name"] != "Dune Chronicles" || body["name_source"] != "folder" {
		t.Fatalf("reverting did not restore the scanned name: %v", body)
	}
}

func TestSeriesRenameRefusals(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "dune", "Dune", []byte("dune"))
	token := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	claimSeries(t, f, token, bookID, "Dune Chronicles")
	id := seriesEntityID(t, f, token, "Dune Chronicles")

	other, _ := f.publishAs(t, "foundation", "Foundation", []byte("foundation"))
	claimSeries(t, f, token, other, "Foundation")

	// Renaming onto an occupied name is a merge request, and merging is
	// not something this route does.
	status, body := f.sendJSON(t, http.MethodPut,
		"/v1/entities/series/"+id+"/name", token,
		map[string]any{"name": "foundation"})
	if status != http.StatusConflict {
		t.Fatalf("renaming onto an occupied name: %d, want 409", status)
	}
	if _, ok := body["error"].(string); !ok {
		t.Errorf("a conflict with no explanation: %v", body)
	}

	for _, name := range []any{"", "   ", strings.Repeat("x", 513)} {
		status, _ := f.sendJSON(t, http.MethodPut,
			"/v1/entities/series/"+id+"/name", token,
			map[string]any{"name": name})
		if status != http.StatusBadRequest {
			t.Errorf("name %q: %d, want 400", name, status)
		}
	}
	status, _ = f.sendJSON(t, http.MethodPut,
		"/v1/entities/series/nope/name", token, map[string]any{"name": "Anything"})
	if status != http.StatusNotFound {
		t.Errorf("renaming a series that does not exist: %d, want 404", status)
	}
	// Only a series has layers to rename in.
	status, _ = f.sendJSON(t, http.MethodPut,
		"/v1/entities/tags/"+id+"/name", token, map[string]any{"name": "Anything"})
	if status != http.StatusNotFound {
		t.Errorf("renaming a tag: %d, want 404", status)
	}
}

func TestSharedSeriesRenameNeedsAdmin(t *testing.T) {
	f := newFolderFixture(t)
	bookID, _ := f.publishAs(t, "dune", "Dune", []byte("dune"))
	boss := f.createUser(t, "rename-boss")
	if err := f.st.SetUserAdmin(t.Context(), boss.ID, true); err != nil {
		t.Fatal(err)
	}
	admin := f.mintToken(t, boss.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage, store.ScopeAdmin)
	reader := f.mintToken(t, f.user.ID,
		store.ScopeLibraryRead, store.ScopeLibraryManage)
	stranger := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)

	// The series has to be one everybody can see, so the claim under it
	// is shared too.
	status, _ := f.sendJSON(t, http.MethodPut, "/v1/books/"+bookID+"/series",
		admin, map[string]any{"scope": "shared", "series": []map[string]any{
			{"name": "Dune Chronicles"},
		}})
	if status != http.StatusOK {
		t.Fatalf("shared claim: %d", status)
	}
	id := seriesEntityID(t, f, admin, "Dune Chronicles")

	body := map[string]any{"scope": "shared", "name": "Chronicles of Dune"}
	if status, _ := f.sendJSON(t, http.MethodPut,
		"/v1/entities/series/"+id+"/name", reader, body); status != http.StatusForbidden {
		t.Fatalf("a non-admin renaming for everybody: %d, want 403", status)
	}
	if status, _ := f.sendJSON(t, http.MethodPut,
		"/v1/entities/series/"+id+"/name", admin, body); status != http.StatusOK {
		t.Fatalf("an admin renaming for everybody: %d", status)
	}
	status, listing := getJSON(t, f.ts.URL+"/v1/entities/series", stranger)
	if status != http.StatusOK {
		t.Fatalf("stranger listing series: %d", status)
	}
	found := false
	for _, e := range listing["entities"].([]any) {
		row := e.(map[string]any)
		if row["name"] == "Chronicles of Dune" {
			found = true
			if row["scanned_name"] != "Dune Chronicles" {
				t.Errorf("the scanned name is not reported: %v", row)
			}
		}
	}
	if !found {
		t.Errorf("the shared rename did not reach another reader: %v", listing)
	}
}
