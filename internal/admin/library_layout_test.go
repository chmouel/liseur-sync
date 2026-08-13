package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

func newLibrary(t *testing.T, st store.Store, owner, name string) string {
	t.Helper()
	out, err := capture(t, st, "create-library", owner, name)
	if err != nil {
		t.Fatal(err)
	}
	return libraryIDFrom(t, out)
}

func TestLibraryLayoutShowsTheDefaultsUntilSet(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")
	id := newLibrary(t, st, "ada", "Ada's books")

	out, err := capture(t, st, "library-layout", "ada", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(the default)") {
		t.Fatalf("an unconfigured library did not say so: %q", out)
	}
	if !strings.Contains(out, metadata.FormatPathPatterns(metadata.DefaultPathPatterns())) {
		t.Fatalf("defaults not shown: %q", out)
	}
	// An operator who has to guess the spelling of a layout cannot set one.
	for _, pattern := range metadata.AllPathPatterns() {
		if !strings.Contains(out, string(pattern)) {
			t.Fatalf("layout %q is not offered: %q", pattern, out)
		}
	}
}

func TestLibraryLayoutSetsAndReadsBackAList(t *testing.T) {
	st := newAdminStore(t)
	owner := addUser(t, st, "ada")
	id := newLibrary(t, st, "ada", "Ada's books")

	out, err := capture(t, st, "library-layout", "ada", id, "series/author-title,author/title")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "series/author-title,author/title") {
		t.Fatalf("what was set is not reported: %q", out)
	}
	if strings.Contains(out, "the default") {
		t.Fatalf("a configured library is described as unconfigured: %q", out)
	}

	library, err := st.LibraryByID(t.Context(), owner.ID, id, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	got, err := metadata.PathPatternsFromConfig(library.Library.ConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	want := []metadata.PathPattern{
		metadata.PatternSeriesAuthorTitle, metadata.PatternAuthorTitle,
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("stored %v, want %v", got, want)
	}
	// Order is meaningful, so the shown list has to be the stored order
	// rather than a canonical one.
	shown, err := capture(t, st, "library-layout", "ada", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "series/author-title,author/title") {
		t.Fatalf("order not preserved: %q", shown)
	}
}

func TestLibraryLayoutNoneDisablesParsingAndDefaultRestoresIt(t *testing.T) {
	st := newAdminStore(t)
	owner := addUser(t, st, "ada")
	id := newLibrary(t, st, "ada", "Ada's books")

	out, err := capture(t, st, "library-layout", "ada", id, "none")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not read") {
		t.Fatalf("disabling is not reported: %q", out)
	}
	library, err := st.LibraryByID(t.Context(), owner.ID, id, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	patterns, err := metadata.PathPatternsFromConfig(library.Library.ConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 0 {
		t.Fatalf("none left %v behind", patterns)
	}

	if _, err := capture(t, st, "library-layout", "ada", id, "default"); err != nil {
		t.Fatal(err)
	}
	library, err = st.LibraryByID(t.Context(), owner.ID, id, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	patterns, err = metadata.PathPatternsFromConfig(library.Library.ConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != len(metadata.DefaultPathPatterns()) {
		t.Fatalf("default restored %v", patterns)
	}
}

// The column holds one document for the whole library. Setting a layout
// must not drop a key a newer server wrote there.
func TestLibraryLayoutPreservesUnrelatedConfiguration(t *testing.T) {
	st := newAdminStore(t)
	owner := addUser(t, st, "ada")
	id := newLibrary(t, st, "ada", "Ada's books")
	if err := st.SetLibraryConfig(t.Context(), owner.ID, id,
		[]byte(`{"future_setting":42}`), timeNowUTC()); err != nil {
		t.Fatal(err)
	}

	if _, err := capture(t, st, "library-layout", "ada", id, "author/title"); err != nil {
		t.Fatal(err)
	}
	library, err := st.LibraryByID(t.Context(), owner.ID, id, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(library.Library.ConfigJSON), `"future_setting":42`) {
		t.Fatalf("unrelated setting lost: %s", library.Library.ConfigJSON)
	}
}

// A library carrying some other setting has still never chosen a layout, so
// it is described as using the defaults rather than as configured.
func TestLibraryLayoutUnrelatedConfigurationIsNotALayoutChoice(t *testing.T) {
	st := newAdminStore(t)
	owner := addUser(t, st, "ada")
	id := newLibrary(t, st, "ada", "Ada's books")
	if err := st.SetLibraryConfig(t.Context(), owner.ID, id,
		[]byte(`{"future_setting":42}`), timeNowUTC()); err != nil {
		t.Fatal(err)
	}

	out, err := capture(t, st, "library-layout", "ada", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(the default)") {
		t.Fatalf("a library with no layout choice was called configured: %q", out)
	}
}

// The two ways to name the wrong thing are a user and a library, and an
// operator has to be able to tell which one they got wrong.
func TestLibraryLayoutErrorsNameWhatWasNotFound(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")
	id := newLibrary(t, st, "ada", "Ada's books")

	_, err := capture(t, st, "library-layout", "nobody", id)
	if err == nil {
		t.Fatal("unknown actor accepted")
	}
	if !strings.Contains(err.Error(), "nobody") || strings.Contains(err.Error(), id) {
		t.Fatalf("unknown actor reported as %q", err)
	}
	for _, args := range [][]string{
		{"library-layout", "ada", "lib-missing"},
		{"library-layout", "ada", "lib-missing", "author/title"},
	} {
		_, err = capture(t, st, args...)
		if err == nil {
			t.Fatalf("%v was accepted", args)
		}
		if !strings.Contains(err.Error(), "lib-missing") ||
			!strings.Contains(err.Error(), "ada") {
			t.Fatalf("unknown library reported as %q", err)
		}
	}
}

func TestLibraryLayoutRejectsBadInput(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")
	id := newLibrary(t, st, "ada", "Ada's books")

	for name, args := range map[string][]string{
		"no arguments":    {"library-layout"},
		"no library":      {"library-layout", "ada"},
		"too many":        {"library-layout", "ada", id, "author/title", "extra"},
		"unknown user":    {"library-layout", "nobody", id},
		"unknown library": {"library-layout", "ada", "lib-missing"},
		"unknown layout":  {"library-layout", "ada", id, "author/isbn"},
		"repeated layout": {"library-layout", "ada", id, "author/title,author/title"},
		"empty list":      {"library-layout", "ada", id, ""},
	} {
		if _, err := capture(t, st, args...); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// A refused command must not have written anything.
	owner, err := st.UserByName(t.Context(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	library, err := st.LibraryByID(t.Context(), owner.ID, id, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if library.Library.ConfigJSON != nil {
		t.Fatalf("a refused command wrote %s", library.Library.ConfigJSON)
	}
}

// An unknown layout is the mistake an operator is most likely to make, so
// the refusal has to say what the choices are.
func TestLibraryLayoutNamesTheChoicesWhenItRefuses(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")
	id := newLibrary(t, st, "ada", "Ada's books")

	_, err := capture(t, st, "library-layout", "ada", id, "author/isbn")
	if err == nil {
		t.Fatal("unknown layout accepted")
	}
	for _, pattern := range metadata.AllPathPatterns() {
		if !strings.Contains(err.Error(), string(pattern)) {
			t.Fatalf("layout %q missing from %q", pattern, err)
		}
	}
}

// Reading the configuration is a manage-level answer: someone who can only
// read the library's books has no business being told, or asked, how it is
// organized.
func TestLibraryLayoutRequiresManageAccess(t *testing.T) {
	st := newAdminStore(t)
	addUser(t, st, "ada")
	addUser(t, st, "bob")
	addUser(t, st, "eve")
	id := newLibrary(t, st, "ada", "Ada's books")
	if _, err := capture(t, st, "grant-library", "ada", id, "bob", "read"); err != nil {
		t.Fatal(err)
	}

	for _, actor := range []string{"bob", "eve"} {
		if _, err := capture(t, st, "library-layout", actor, id); err == nil {
			t.Fatalf("%s was shown the layout", actor)
		}
		if _, err := capture(t, st, "library-layout", actor, id, "none"); err == nil {
			t.Fatalf("%s set the layout", actor)
		}
	}
	owner, err := st.UserByName(t.Context(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	library, err := st.LibraryByID(t.Context(), owner.ID, id, store.LibraryRoleManage)
	if err != nil {
		t.Fatal(err)
	}
	if library.Library.ConfigJSON != nil {
		t.Fatalf("a refused write left %s", library.Library.ConfigJSON)
	}

	// A manager may do both.
	if _, err := capture(t, st, "grant-library", "ada", id, "bob", "manage"); err != nil {
		t.Fatal(err)
	}
	if _, err := capture(t, st, "library-layout", "bob", id, "author/title"); err != nil {
		t.Fatalf("manager refused: %v", err)
	}
}

// A configuration this server cannot read is the reason the library's books
// are not being described, so the command has to report it rather than
// print the defaults it is not using.
func TestLibraryLayoutReportsAnUnreadableConfiguration(t *testing.T) {
	st := newAdminStore(t)
	owner := addUser(t, st, "ada")
	id := newLibrary(t, st, "ada", "Ada's books")
	if err := st.SetLibraryConfig(t.Context(), owner.ID, id,
		[]byte(`{"path_patterns":["author/isbn"]}`), timeNowUTC()); err != nil {
		t.Fatal(err)
	}

	out, err := capture(t, st, "library-layout", "ada", id)
	if err == nil {
		t.Fatalf("an unreadable configuration was reported as %q", out)
	}
	if !strings.Contains(err.Error(), "author/isbn") {
		t.Fatalf("error does not name the problem: %v", err)
	}
	// Setting a valid list is the way out, so it must not be blocked by the
	// document it is replacing.
	if _, err := capture(t, st, "library-layout", "ada", id, "default"); err != nil {
		t.Fatalf("cannot repair an unreadable configuration: %v", err)
	}
	if _, err := capture(t, st, "library-layout", "ada", id); err != nil {
		t.Fatalf("still unreadable after repair: %v", err)
	}
}

func timeNowUTC() time.Time { return time.Now().UTC() }
