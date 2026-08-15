package webui

import (
	"context"
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
// at a shell: attaching a root-backed library, minting somebody else's
// credentials, working a review queue and checking the backup.

// generousReauth widens the password re-verification budget for the
// tests that walk several gated actions in a row. The limit itself is
// asserted by TestAdminReauthIsRateLimited.
func generousReauth(s *Server) {
	s.AdminReauthUserLimiter = auth.NewRateLimiter(100, time.Minute)
	s.AdminReauthIPLimiter = auth.NewRateLimiter(100, time.Minute)
}

// TestAdminAttachesARootLibrary covers `add-library` from the panel,
// with all three axes on the form and the guards ADR-0013 asks for.
func TestAdminAttachesARootLibrary(t *testing.T) {
	// A generous re-verification budget: this walks six actions that
	// each cost the administrator their password, which is more than
	// one sitting would, not a sign the limit is wrong.
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	mkTarget(t, st, "bob")
	root := t.TempDir()

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/admin/libraries")
	csrf := extractCSRF(t, body)
	if !strings.Contains(body, "Add a library") {
		t.Fatalf("the add form is not on the page:\n%s", body)
	}

	// A wrong password is refused before anything is looked at on disk.
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Shelf"}, "root": {root},
		"admin_password": {"not-it"},
	})
	if !strings.Contains(body, "your password is wrong") {
		t.Fatalf("a wrong password created a library: %s", body)
	}

	// Checking a directory reports on it and creates nothing.
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Shelf"}, "root": {root},
		"action": {"check"}, "admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "would add it as a folder of books") {
		t.Fatalf("check: %s", body)
	}
	if libs, _ := st.AdminListLibraries(ctx, "", 10); len(libs) != 0 {
		t.Fatalf("checking a directory created %d libraries", len(libs))
	}

	// A path that is not a directory says so without describing the
	// filesystem beyond what was typed.
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Shelf"},
		"root": {filepath.Join(root, "nope")}, "admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "cannot read a folder at that path") {
		t.Fatalf("missing directory: %s", body)
	}

	// Forcing the source is the advanced override: asking for Calibre on
	// a tree with no metadata.db is a directory somebody pointed at by
	// mistake.
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Calibre"}, "root": {root},
		"source": {"calibre"}, "admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "metadata.db") {
		t.Fatalf("calibre without a database: %s", body)
	}

	// The real thing, with every axis set by hand.
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Shelf"}, "root": {root},
		"source": {"directory"}, "storage": {"in-place"},
		"refresh":        {"30m"},
		"admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "Added Shelf for bob as a folder of books") {
		t.Fatalf("attach: %s", body)
	}
	libs, err := st.AdminListLibraries(ctx, "", 10)
	if err != nil || len(libs) != 1 {
		t.Fatalf("libraries = %d, %v", len(libs), err)
	}
	lib := libs[0]
	switch {
	case lib.Source != store.LibraryDirectory:
		t.Fatalf("source = %q", lib.Source)
	case lib.Storage != store.LibraryStorageInPlace:
		t.Fatalf("storage = %q", lib.Storage)
	case lib.Refresh != store.LibraryRefreshInterval:
		t.Fatalf("refresh = %q", lib.Refresh)
	case lib.RefreshInterval != 30*time.Minute:
		t.Fatalf("interval = %s", lib.RefreshInterval)
	case lib.RootPath == nil || *lib.RootPath != root:
		t.Fatalf("root = %v", lib.RootPath)
	case lib.OwnerUserID != "bob-id":
		t.Fatalf("owner = %q", lib.OwnerUserID)
	}

	// The simple flow names no source at all: a tree holding
	// metadata.db is recognized as a Calibre library, checked as one,
	// and read where it lies.
	calibre := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(calibre, "metadata.db"), []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Calibre"}, "root": {calibre},
		"action": {"check"}, "admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "would add it as a Calibre library") {
		t.Fatalf("check calibre: %s", body)
	}
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Calibre"}, "root": {calibre},
		"refresh":        {"manual"},
		"admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "Added Calibre for bob as a Calibre library") {
		t.Fatalf("attach calibre: %s", body)
	}
	libs, _ = st.AdminListLibraries(ctx, "", 10)
	var found bool
	for _, l := range libs {
		if l.Source != store.LibraryCalibre {
			continue
		}
		found = true
		if l.Storage != store.LibraryStorageInPlace {
			t.Fatalf("a Calibre library was copied: %q", l.Storage)
		}
		// The interval column keeps its default even when nothing
		// sweeps on it; what matters is that nothing does.
		if l.Refresh != store.LibraryRefreshManual {
			t.Fatalf("refresh = %q", l.Refresh)
		}
	}
	if !found {
		t.Fatal("the Calibre library was not created")
	}

	// The override beats the sniff: a tree with metadata.db attached
	// as a plain directory stays a plain directory.
	mixed := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(mixed, "metadata.db"), []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Flat"}, "root": {mixed},
		"source":         {"directory"},
		"admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "Added Flat for bob as a folder of books") {
		t.Fatalf("attach override: %s", body)
	}
}

// TestAdminRootLibraryHonoursTheAllowlist covers content.library_roots:
// with one set, the form is a choice among the trees an operator meant
// to serve rather than the whole disk.
func TestAdminRootLibraryHonoursTheAllowlist(t *testing.T) {
	allowed := t.TempDir()
	inside := filepath.Join(allowed, "shelf")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	ts, st := testServerCfg(t, func(c *config.Config) {
		c.Content.LibraryRoots = []string{allowed}
	}, generousReauth)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	mkTarget(t, st, "bob")

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/admin/libraries")
	csrf := extractCSRF(t, body)
	if !strings.Contains(body, allowed) {
		t.Fatalf("the page does not say where libraries may live:\n%s", body)
	}
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Nope"}, "root": {elsewhere},
		"admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "content.library_roots") {
		t.Fatalf("a root outside the allowlist was accepted: %s", body)
	}
	if libs, _ := st.AdminListLibraries(t.Context(), "", 10); len(libs) != 0 {
		t.Fatalf("a refused root still created %d libraries", len(libs))
	}
	// A directory below an allowed root is allowed.
	_, body = postForm(t, ts, cookie, "/ui/admin/libraries", url.Values{
		"csrf": {csrf}, "from": {"folder"}, "owner": {"bob"}, "name": {"Shelf"}, "root": {inside},
		"admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "Added Shelf for bob as a folder of books") {
		t.Fatalf("a root below an allowed one was refused: %s", body)
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

// TestAdminWorksAReviewQueue covers `list-review` and `clear-review`
// from the panel — including that the queue names no book of somebody
// else's, which is the boundary the whole section rests on.
func TestAdminWorksAReviewQueue(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	root := t.TempDir()
	now := time.Now().UTC()
	if err := st.CreateLibrary(ctx, store.Library{
		ID: "lib-bob", OwnerUserID: bob.ID, QuotaUserID: bob.ID,
		Source: store.LibraryDirectory, Storage: store.LibraryStorageCAS,
		Refresh: store.LibraryRefreshManual, Name: "Bobs shelf",
		RootPath: &root, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	const secretTitle = "Bobs Very Private Diary"
	if err := st.CreateCatalogBook(ctx, bob.ID, store.CatalogBook{
		ID: "book-1", LibraryID: "lib-bob", Status: store.BookActive,
		Title: secretTitle, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := st.SetCatalogBookReview(
		ctx, "lib-bob", "book-1", "the file at this watched path was replaced", now)
	if err != nil || !changed {
		t.Fatalf("flag for review: %v %v", changed, err)
	}

	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/admin/libraries/lib-bob/review")
	if code != 200 {
		t.Fatalf("review page: %d", code)
	}
	if !strings.Contains(body, "book-1") || !strings.Contains(body, "was replaced") {
		t.Fatalf("the queue does not say what is waiting or why:\n%s", body)
	}
	if strings.Contains(body, secretTitle) {
		t.Fatalf("the admin review queue names somebody else's book:\n%s", body)
	}
	csrf := extractCSRF(t, body)

	_, body = postForm(t, ts, cookie,
		"/ui/admin/libraries/lib-bob/review/book-1/clear", url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "Cleared.") {
		t.Fatalf("clear: %s", body)
	}
	if !strings.Contains(body, "Nothing is awaiting review") {
		t.Fatalf("the cleared book is still in the queue:\n%s", body)
	}
	// Clearing twice says so rather than claiming to have done it again.
	_, body = postForm(t, ts, cookie,
		"/ui/admin/libraries/lib-bob/review/book-1/clear", url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "was not awaiting review") {
		t.Fatalf("second clear: %s", body)
	}
}

// fakeVerifier stands in for the content store's backup check, which
// needs a real content directory this package deliberately does not
// have.
type fakeVerifier struct {
	report BackupReport
	err    error
	done   chan struct{}
}

func (f *fakeVerifier) VerifyBackup(context.Context) (BackupReport, error) {
	if f.done != nil {
		close(f.done)
	}
	return f.report, f.err
}

// TestAdminVerifiesTheBackup covers `verify-backup` from the
// maintenance page: it runs out of band and reports the last result.
func TestAdminVerifiesTheBackup(t *testing.T) {
	verifier := &fakeVerifier{
		report: BackupReport{
			ReferencedBlobs: 12, PresentBlobs: 11, MissingBlobs: 1,
			Problems: []string{"abc123 not in the backup"}, More: 3,
		},
		done: make(chan struct{}),
	}
	ts, st := testServerCfg(t, nil, func(s *Server) { s.Backups = verifier })
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/admin/maintenance")
	if code != 200 || !strings.Contains(body, "Backup check") {
		t.Fatalf("maintenance page: %d\n%s", code, body)
	}
	csrf := extractCSRF(t, body)

	_, body = postForm(t, ts, cookie, "/ui/admin/maintenance/verify",
		url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "Verifying.") {
		t.Fatalf("verify: %s", body)
	}
	select {
	case <-verifier.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the verification never ran")
	}
	// The run finishes just after the goroutine returns; poll the page
	// rather than sleeping a fixed amount.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, body = page(t, ts, cookie, "/ui/admin/maintenance")
		if strings.Contains(body, "not restorable") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the result never reached the page:\n%s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, want := range []string{"abc123", "and 3 more", "Referenced files"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the report is missing %q:\n%s", want, body)
		}
	}
}

// TestAdminBackupCheckReportsItsAbsence covers the build with no
// content store: the page says so rather than offering a button that
// cannot work.
func TestAdminBackupCheckReportsItsAbsence(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/admin/maintenance")
	if !strings.Contains(body, "no content directory to check") {
		t.Fatalf("maintenance page without a verifier:\n%s", body)
	}
	csrf := extractCSRF(t, body)
	_, body = postForm(t, ts, cookie, "/ui/admin/maintenance/verify",
		url.Values{"csrf": {csrf}})
	if !strings.Contains(body, "no content directory to verify") {
		t.Fatalf("verify without a verifier: %s", body)
	}
}

// TestAdminBackupRunIsExclusive: a second press while one is running is
// refused rather than queued, so two passes never share the disk.
func TestAdminBackupRunIsExclusive(t *testing.T) {
	release := make(chan struct{})
	blocking := &blockingVerifier{
		release: release, started: make(chan struct{}),
	}
	ts, st := testServerCfg(t, nil, func(s *Server) { s.Backups = blocking })
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/admin/maintenance")
	csrf := extractCSRF(t, body)
	if _, b := postForm(t, ts, cookie, "/ui/admin/maintenance/verify",
		url.Values{"csrf": {csrf}}); !strings.Contains(b, "Verifying.") {
		t.Fatalf("first run: %s", b)
	}
	<-blocking.started
	if _, b := postForm(t, ts, cookie, "/ui/admin/maintenance/verify",
		url.Values{"csrf": {csrf}}); !strings.Contains(b, "already running") {
		t.Fatalf("second run: %s", b)
	}
	close(release)
}

type blockingVerifier struct {
	release chan struct{}
	started chan struct{}
}

func (b *blockingVerifier) VerifyBackup(context.Context) (BackupReport, error) {
	close(b.started)
	<-b.release
	return BackupReport{Valid: true}, nil
}
