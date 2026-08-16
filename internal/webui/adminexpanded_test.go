package webui

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The panel is meant to be a superset of the `admin` subcommands, not a
// subset of them. These tests walk the controls that used to exist only
// at a shell: watching a folder, letting one go, and minting somebody
// else's credentials.

// generousReauth widens the password re-verification budget for the
// tests that walk several gated actions in a row. The limit itself is
// asserted by TestAdminReauthIsRateLimited.
func generousReauth(s *Server) {
	s.AdminReauthUserLimiter = auth.NewRateLimiter(100, time.Minute)
	s.AdminReauthIPLimiter = auth.NewRateLimiter(100, time.Minute)
}

// TestAdminWatchesAndForgetsAFolder covers `add-folder` and
// `remove-folder` from the panel. A folder has one axis — where it is —
// because its kind is read off the disk and there is nobody to own it
// (ADR-0017).
func TestAdminWatchesAndForgetsAFolder(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/admin/folders")
	csrf := extractCSRF(t, body)
	if !strings.Contains(body, "Watch a folder") {
		t.Fatalf("the add form is not on the page:\n%s", body)
	}

	// A blank name is refused, and refused by the same rule the CLI
	// uses rather than by a second copy of it.
	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"  "}, "root": {root},
	})
	if !strings.Contains(body, "a folder name is required") {
		t.Fatalf("a blank name was accepted: %s", body)
	}

	// A path that is not a directory is refused before a row exists.
	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"Nowhere"},
		"root": {filepath.Join(root, "does-not-exist")},
	})
	if strings.Contains(body, "Watching Nowhere") {
		t.Fatalf("a missing directory was accepted: %s", body)
	}
	if folders, _ := st.ListFolders(ctx, "", 10); len(folders) != 0 {
		t.Fatalf("a refused add created %d folders", len(folders))
	}

	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"Shelf"}, "root": {root},
	})
	if !strings.Contains(body, "Watching Shelf") {
		t.Fatalf("add: %s", body)
	}
	folders, err := st.ListFolders(ctx, "", 10)
	if err != nil || len(folders) != 1 {
		t.Fatalf("folders = %d, %v", len(folders), err)
	}
	if folders[0].Kind != store.FolderPlain {
		t.Errorf("kind = %q, want plain for a directory of files", folders[0].Kind)
	}

	// The root path is on this page, and this page only: it is a
	// filesystem oracle and no part of reading a catalog.
	_, body = page(t, ts, cookie, "/ui/admin/folders")
	if !strings.Contains(body, root) {
		t.Fatalf("the folder list does not say where the folder is:\n%s", body)
	}
	_, shelf := page(t, ts, cookie, "/ui/library")
	if strings.Contains(shelf, root) {
		t.Errorf("the reader-facing shelf leaked a root path:\n%s", shelf)
	}

	// Forgetting a folder is a form post with the session's token, and
	// it says plainly that it changed nothing on disk.
	csrf = extractCSRF(t, body)
	_, body = postForm(t, ts, cookie,
		"/ui/admin/folders/"+folders[0].ID+"/delete", url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "Stopped watching Shelf") {
		t.Fatalf("delete: %s", body)
	}
	if folders, _ := st.ListFolders(ctx, "", 10); len(folders) != 0 {
		t.Fatalf("a removed folder is still listed: %v", folders)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("removing a folder touched the disk: %v", err)
	}
}

// TestAdminFolderHonoursTheAllowlist: when an operator has said where
// folders may live, the panel is bounded by it. Adding a folder is a
// privilege beyond administering the application, since it names a
// directory on the host.
func TestAdminFolderHonoursTheAllowlist(t *testing.T) {
	allowed := t.TempDir()
	inside := filepath.Join(allowed, "shelf")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	ts, st := testServerCfg(t, func(c *config.Config) {
		c.Content.FolderRoots = []string{allowed}
	}, generousReauth)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/admin/folders")
	csrf := extractCSRF(t, body)
	if !strings.Contains(body, allowed) {
		t.Fatalf("the page does not say where folders may live:\n%s", body)
	}
	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"Nope"}, "root": {elsewhere},
	})
	if !strings.Contains(body, "not below any of the roots") {
		t.Fatalf("a root outside the allowlist was accepted: %s", body)
	}
	if folders, _ := st.ListFolders(t.Context(), "", 10); len(folders) != 0 {
		t.Fatalf("a refused root still created %d folders", len(folders))
	}
	// A directory below an allowed root is allowed.
	_, body = postForm(t, ts, cookie, "/ui/admin/folders", url.Values{
		"csrf": {csrf}, "name": {"Shelf"}, "root": {inside},
	})
	if !strings.Contains(body, "Watching Shelf") {
		t.Fatalf("a root below an allowed one was refused: %s", body)
	}
}

// TestAdminFolderMutationsNeedTheSessionsToken: the two mutations here
// name a directory on the host, so a page on another origin must not be
// able to drive either of them.
func TestAdminFolderMutationsNeedTheSessionsToken(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	folder := store.Folder{
		ID: "f-csrf", Name: "Shelf", RootPath: root,
		Kind: store.FolderPlain, CreatedAt: time.Now().UTC(),
	}
	if err := st.CreateFolder(ctx, folder); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	for _, path := range []string{
		"/ui/admin/folders",
		"/ui/admin/folders/" + folder.ID + "/scan",
		"/ui/admin/folders/" + folder.ID + "/delete",
	} {
		code, _ := postForm(t, ts, cookie, path, url.Values{
			"csrf": {"forged"}, "name": {"Elsewhere"}, "root": {root},
		})
		if code != 403 {
			t.Errorf("%s without a token: %d, want 403", path, code)
		}
	}
	if folders, _ := st.ListFolders(ctx, "", 10); len(folders) != 1 {
		t.Fatalf("a forged request changed the folder list: %v", folders)
	}
}

// TestAdminMintsCredentialsForAnotherAccount covers `mint-token`,
// `pairing-code` and `koplugin-device` from the panel: each hands out a
// working way into somebody else's account, so each is behind the
// acting administrator's own password and shows its secret once.
func TestAdminMintsCredentialsForAnotherAccount(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/admin/users/"+bob.ID)
	csrf := extractCSRF(t, body)

	// Without the admin's password, nothing is minted.
	_, body = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/tokens", url.Values{
		"csrf": {csrf}, "name": {"Boox"}, "scopes": {"sync"},
		"admin_password": {"wrong"},
	})
	if !strings.Contains(body, "your password is wrong") {
		t.Fatalf("mint without a password: %s", body)
	}
	if toks, _ := st.ListTokens(ctx, bob.ID); len(toks) != 0 {
		t.Fatalf("a refused mint created %d tokens", len(toks))
	}

	_, body = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/tokens", url.Values{
		"csrf": {csrf}, "name": {"Boox"}, "scopes": {"sync", "library-read"},
		"admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "shown once") {
		t.Fatalf("mint: %s", body)
	}
	toks, err := st.ListTokens(ctx, bob.ID)
	if err != nil || len(toks) != 1 {
		t.Fatalf("tokens = %d, %v", len(toks), err)
	}
	if got := toks[0].Scopes.String(); !strings.Contains(got, "library-read") {
		t.Fatalf("scopes = %q", got)
	}

	// The admin scope is not self-grantable through this form either.
	_, body = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/tokens", url.Values{
		"csrf": {csrf}, "name": {"Root"}, "scopes": {"admin"},
		"admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "administrator") {
		t.Fatalf("admin scope on an ordinary account: %s", body)
	}

	_, body = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/pairing", url.Values{
		"csrf": {csrf}, "admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "kosync pairing code for bob") {
		t.Fatalf("pairing: %s", body)
	}

	_, body = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/koplugin", url.Values{
		"csrf": {csrf}, "name": {"clara"}, "admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "Capability for bob") {
		t.Fatalf("koplugin: %s", body)
	}
	devs, err := st.ListKopluginDevices(ctx, bob.ID)
	if err != nil || len(devs) != 1 || devs[0].Label != "clara" {
		t.Fatalf("koplugin devices = %v, %v", devs, err)
	}

	// Backfill reports counts and needs no re-verification: it hands
	// out nothing.
	_, body = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/backfill", url.Values{
		"csrf": {csrf},
	})
	if !strings.Contains(body, "Mapped bob&#39;s books to works") {
		t.Fatalf("backfill: %s", body)
	}
}

// TestAdminMaintenanceReportsWhatIsStuck: the page that replaced the
// queue board counts folders and book statuses, and nothing on it names
// a book, a path or an account.
func TestAdminMaintenanceReportsWhatIsStuck(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := st.CreateFolder(ctx, store.Folder{
		ID: "f-maint", Name: "Shelf", RootPath: root,
		Kind: store.FolderPlain, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	code, body := page(t, ts, loginCookie(t, ts), "/ui/admin/maintenance")
	if code != 200 {
		t.Fatalf("maintenance: %d", code)
	}
	for _, want := range []string{"Folders", "plain", "calibre", "active", "missing"} {
		if !strings.Contains(body, want) {
			t.Errorf("maintenance page does not report %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, root) || strings.Contains(body, "Shelf") {
		t.Errorf("maintenance named a folder rather than counting one:\n%s", body)
	}
}
