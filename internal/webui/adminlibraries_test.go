package webui

import (
	"strings"
	"testing"
	"time"

	"net/url"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// TestAdminLibrariesPage walks phase 4: the page shows every library on
// the instance without the administrator holding a grant on any of
// them, creates a managed one, moves access around, and sets a layout.
func TestAdminLibrariesPage(t *testing.T) {
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
		Refresh: store.LibraryRefreshInterval, Name: "Bobs shelf", RootPath: &root,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/admin/libraries")
	if code != 200 {
		t.Fatalf("libraries page: %d", code)
	}
	for _, want := range []string{"Bobs shelf", "directory · cas", "bob", root, "Only the owner."} {
		if !strings.Contains(body, want) {
			t.Fatalf("libraries page is missing %q:\n%s", want, body)
		}
	}
	csrf := extractCSRF(t, body)

	// Creating a managed library, and the refusals around it.
	if _, b := postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "owner": {"nobody"}, "name": {"Shelf"},
	}); !strings.Contains(b, "no account by that name") {
		t.Fatalf("unknown owner: %s", b)
	}
	if _, b := postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "owner": {"bob"}, "name": {"   "},
	}); !strings.Contains(b, "library name is required") {
		t.Fatalf("blank name: %s", b)
	}
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "owner": {"bob"}, "name": {"Comics"},
	})
	if !strings.Contains(body, "Created Comics for bob.") {
		t.Fatalf("create library: %s", body)
	}

	// Access: granting the owner is refused, granting somebody else
	// works and is real, and "no access" takes it back.
	if _, b := postForm(t, ts, cookie, "/ui/admin/libraries/lib-bob/access", url.Values{
		"csrf": {csrf}, "user": {"bob"}, "role": {"read"},
	}); !strings.Contains(b, "owner already has full access") {
		t.Fatalf("granting the owner: %s", b)
	}
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries/lib-bob/access", url.Values{
		"csrf": {csrf}, "user": {"alice"}, "role": {"read"},
	})
	if !strings.Contains(body, "alice can now read this library.") {
		t.Fatalf("grant: %s", body)
	}
	if _, err := st.LibraryByID(ctx, "u1", "lib-bob", store.LibraryRoleRead); err != nil {
		t.Fatalf("the grant did not take: %v", err)
	}
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries/lib-bob/access", url.Values{
		"csrf": {csrf}, "user": {"alice"}, "role": {"none"},
	})
	if !strings.Contains(body, "alice can no longer reach this library.") {
		t.Fatalf("revoke: %s", body)
	}
	if _, err := st.LibraryByID(ctx, "u1", "lib-bob", store.LibraryRoleRead); err == nil {
		t.Fatal("revoked access still reads")
	}

	// Layout: a chosen one sticks, and choosing none restores the
	// default rather than meaning "read no filename at all".
	want := string(metadata.PatternAuthorSeriesTitle)
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries/lib-bob/layout", url.Values{
		"csrf": {csrf}, "layout": {want},
	})
	if !strings.Contains(body, "now reads filenames as "+want) {
		t.Fatalf("set layout: %s", body)
	}
	lib, err := st.AdminLibraryByID(ctx, "lib-bob")
	if err != nil {
		t.Fatal(err)
	}
	got, configured, err := metadataLayouts(lib)
	if err != nil || !configured || len(got) != 1 || string(got[0]) != want {
		t.Fatalf("stored layout = %v configured=%v err=%v", got, configured, err)
	}
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries/lib-bob/layout", url.Values{
		"csrf": {csrf},
	})
	if !strings.Contains(body, "back to the default filename layouts.") {
		t.Fatalf("clear layout: %s", body)
	}
	lib, _ = st.AdminLibraryByID(ctx, "lib-bob")
	if _, configured, _ := metadataLayouts(lib); configured {
		t.Fatal("clearing the layout left it configured")
	}

	// A mutation with no CSRF token is refused before anything else.
	if code, _ := postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"owner": {"bob"}, "name": {"Nope"},
	}); code != 403 {
		t.Fatalf("missing CSRF: want 403, got %d", code)
	}

	// The per-user page names what an account can reach.
	code, body = page(t, ts, cookie, "/ui/admin/users/"+bob.ID)
	if code != 200 || !strings.Contains(body, "Bobs shelf") || !strings.Contains(body, "owner") {
		t.Fatalf("per-user libraries section: %d\n%s", code, body)
	}
}

// metadataLayouts is the test's own reader of a stored configuration,
// so that a wrong answer from the shared helper cannot agree with a
// wrong answer from the handler.
func metadataLayouts(lib store.Library) ([]metadata.PathPattern, bool, error) {
	patterns, err := metadata.PathPatternsFromConfig(lib.ConfigJSON)
	if err != nil {
		return nil, false, err
	}
	configured, err := metadata.PathPatternsConfigured(lib.ConfigJSON)
	return patterns, configured, err
}

// TestAdminMaintenancePage pins phase 5: the page reports aggregate
// worker state, and reports nothing that identifies a book or an
// account.
func TestAdminMaintenancePage(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/admin/maintenance")
	if code != 200 {
		t.Fatalf("maintenance page: %d", code)
	}
	for _, want := range []string{
		"Ingest", "pending", "running", "Held for review", "In trash",
		"Orphaned", "Blobs",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("maintenance page is missing %q", want)
		}
	}
	// Aggregate only: no "run now" control anywhere on it.
	for _, unwanted := range []string{"Run now", "run-now"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("maintenance page offers %q", unwanted)
		}
	}
}

func TestAgeAndUntil(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5 minutes"},
		{time.Hour, "1 hour"},
		{50 * time.Hour, "2 days"},
	} {
		if got := age(tc.d); got != tc.want {
			t.Errorf("age(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
	if got := until(nil, now); got != "" {
		t.Errorf("until(nil) = %q", got)
	}
	past := now.Add(-time.Hour)
	if got := until(&past, now); got != "due now" {
		t.Errorf("until(past) = %q", got)
	}
	future := now.Add(3 * time.Hour)
	if got := until(&future, now); got != "in 3 hours" {
		t.Errorf("until(future) = %q", got)
	}
}
