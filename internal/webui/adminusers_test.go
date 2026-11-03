package webui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// postNoFollow posts a form and hands back the response itself, for the
// tests that are about the status and the Location rather than about the
// page at the end of the redirect.
func postNoFollow(
	t *testing.T, ts *httptest.Server, cookie *http.Cookie, path string, form url.Values,
) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// nestedFormDepth counts the forms a page nests inside another form.
// Browsers drop the inner tag when they meet it, so the damage is a form
// whose fields silently go nowhere — which is why the per-user page puts
// its forms behind disclosures instead of inside each other.
func nestedFormDepth(t *testing.T, page string) int {
	t.Helper()
	z := html.NewTokenizer(strings.NewReader(page))
	depth, nested := 0, 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return nested
		case html.StartTagToken:
			if name, _ := z.TagName(); string(name) == "form" {
				depth++
				if depth > 1 {
					nested++
				}
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "form" && depth > 0 {
				depth--
			}
		}
	}
}

func mkTarget(t *testing.T, st store.Store, name string) store.User {
	t.Helper()
	hash, _ := auth.HashPassword("bobs-password")
	u := store.User{
		ID: name + "-id", Name: name, Argon2Hash: hash, Timezone: "UTC",
		KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now(),
	}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAdminAssignsFolderAccessIncludingSelf(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	folder := store.Folder{
		ID: "folder-access", Name: "Private Shelf", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := st.CreateFolder(t.Context(), folder); err != nil {
		t.Fatal(err)
	}
	for i := range adminFoldersPerPage {
		paged := store.Folder{
			ID: store.NewID(), Name: fmt.Sprintf("Paged Shelf %02d", i),
			RootPath: t.TempDir(), Kind: store.FolderPlain,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := st.CreateFolder(t.Context(), paged); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginCookie(t, ts)
	path := "/ui/settings?section=admin&view=user&user=" + bob.ID
	_, body := page(t, ts, cookie, path)
	if !strings.Contains(body, "Private Shelf") ||
		!strings.Contains(body, "Paged Shelf 49") ||
		!strings.Contains(body, "Folder access") {
		t.Fatalf("folder access form missing: %s", body)
	}
	csrf := extractCSRF(t, body)
	postPath := "/ui/admin/users/" + bob.ID + "/folders"
	if code, _ := postForm(t, ts, cookie, postPath,
		url.Values{"folders": {folder.ID}}); code != http.StatusForbidden {
		t.Fatalf("assignment without CSRF = %d", code)
	}
	if code, _ := postForm(t, ts, cookie, postPath,
		url.Values{"csrf": {csrf}, "folders": {folder.ID}}); code != http.StatusOK {
		t.Fatalf("assignment = %d", code)
	}
	if _, err := st.FolderByID(t.Context(), bob.ID, folder.ID); err != nil {
		t.Fatalf("bob did not receive the folder: %v", err)
	}

	selfPath := "/ui/settings?section=admin&view=user&user=u1"
	_, body = page(t, ts, cookie, selfPath)
	csrf = extractCSRF(t, body)
	if code, _ := postForm(t, ts, cookie, "/ui/admin/users/u1/folders",
		url.Values{"csrf": {csrf}, "folders": {folder.ID}}); code != http.StatusOK {
		t.Fatalf("self-assignment = %d", code)
	}
	if _, err := st.FolderByID(t.Context(), "u1", folder.ID); err != nil {
		t.Fatalf("admin did not receive their self-grant: %v", err)
	}
	if code, _ := postForm(t, ts, cookie, postPath,
		url.Values{"csrf": {csrf}}); code != http.StatusOK {
		t.Fatalf("empty replacement = %d", code)
	}
	if _, err := st.FolderByID(t.Context(), bob.ID, folder.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("empty replacement left access: %v", err)
	}
}

// TestAdminFolderGrantIgnoresQueryStringFolders pins the grant form to
// the request body. r.Form merges the URL query into the parsed body, so
// reading it would let a crafted link decide which folders a submitted
// form grants — the admin sees their own checkboxes and saves something
// else.
func TestAdminFolderGrantIgnoresQueryStringFolders(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	shown := store.Folder{
		ID: "shown", Name: "Shown Shelf", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	smuggled := store.Folder{
		ID: "smuggled", Name: "Smuggled Shelf", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	for _, f := range []store.Folder{shown, smuggled} {
		if err := st.CreateFolder(t.Context(), f); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie,
		"/ui/settings?section=admin&view=user&user="+bob.ID)
	csrf := extractCSRF(t, body)
	postPath := "/ui/admin/users/" + bob.ID + "/folders?folders=" + smuggled.ID
	if code, _ := postForm(t, ts, cookie, postPath,
		url.Values{"csrf": {csrf}, "folders": {shown.ID}}); code != http.StatusOK {
		t.Fatalf("assignment = %d", code)
	}
	if _, err := st.FolderByID(t.Context(), bob.ID, shown.ID); err != nil {
		t.Fatalf("the submitted folder was not granted: %v", err)
	}
	if _, err := st.FolderByID(t.Context(), bob.ID, smuggled.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a query-string folder was granted: %v", err)
	}
}

// TestAllFoldersReportsOverflowRatherThanAPrefix covers the bound on the
// grant form's folder list. A prefix would be worse than an error: the
// form replaces the whole grant set, so saving a truncated page would
// revoke every folder past the cut.
func TestAllFoldersReportsOverflowRatherThanAPrefix(t *testing.T) {
	_, st := testServer(t)
	total := adminFoldersPerPage + 5
	for i := range total {
		f := store.Folder{
			ID: store.NewID(), Name: fmt.Sprintf("Shelf %03d", i),
			RootPath: t.TempDir(), Kind: store.FolderPlain,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := st.CreateFolder(t.Context(), f); err != nil {
			t.Fatal(err)
		}
	}
	all, tooMany, err := allFolders(t.Context(), st, "", adminUserFoldersMax)
	if err != nil {
		t.Fatal(err)
	}
	if tooMany || len(all) != total {
		t.Fatalf("under the cap: tooMany=%v len=%d want %d", tooMany, len(all), total)
	}
	all, tooMany, err = allFolders(t.Context(), st, "", adminFoldersPerPage-1)
	if err != nil {
		t.Fatal(err)
	}
	if !tooMany {
		t.Fatalf("over the cap returned %d folders instead of an overflow", len(all))
	}
	if all != nil {
		t.Fatalf("overflow returned a prefix of %d folders", len(all))
	}
}

// TestAdminUserPageAndCredentialRevocation walks the per-user page: it
// lists the account's credentials, revokes one of each kind, and then
// revokes everything at once. The single-credential revocations are not
// behind the admin's password; the sweep is.
func TestAdminUserPageAndCredentialRevocation(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	ctx := t.Context()
	if err := st.CreateToken(ctx, store.Token{
		ID: "tok-1", UserID: bob.ID, DeviceID: "d1", Name: "Boox Palma",
		Scopes: store.ScopeSet{store.ScopeSync}, SHA256: "tok-sha",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateKosyncDevice(ctx, store.KosyncDevice{
		UserID: bob.ID, DeviceSlot: "slot-1", KeySHA256: "kosync-sha", Label: "kobo",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateKopluginDevice(ctx, store.KopluginDevice{
		ID: "kop-1", UserID: bob.ID, TokenSHA256: "kop-sha", Label: "clara",
		DeviceID: "dev-1", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/settings?section=admin&view=user&user="+bob.ID)
	if code != 200 {
		t.Fatalf("user page: %d", code)
	}
	for _, want := range []string{"Boox Palma", "slot-1", "clara", "Revoke everything"} {
		if !strings.Contains(body, want) {
			t.Fatalf("user page is missing %q", want)
		}
	}
	csrf := extractCSRF(t, body)

	// A mutation without the CSRF token is refused before anything else.
	if c, _ := postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/tokens/tok-1/revoke",
		url.Values{}); c != 403 {
		t.Fatalf("revoke without CSRF: want 403, got %d", c)
	}

	for _, path := range []string{
		"/ui/admin/users/" + bob.ID + "/tokens/tok-1/revoke",
		"/ui/admin/users/" + bob.ID + "/kosync/slot-1/revoke",
		"/ui/admin/users/" + bob.ID + "/koplugin/kop-1/revoke",
	} {
		if c, _ := postForm(t, ts, cookie, path, url.Values{"csrf": {csrf}}); c != 200 {
			t.Fatalf("POST %s: %d", path, c)
		}
	}
	toks, _ := st.ListTokens(ctx, bob.ID)
	if len(toks) != 1 || toks[0].RevokedAt == nil {
		t.Fatalf("token not revoked: %+v", toks)
	}
	kos, _ := st.ListKosyncDevices(ctx, bob.ID)
	if len(kos) != 1 || kos[0].RevokedAt == nil {
		t.Fatalf("kosync slot not revoked: %+v", kos)
	}
	kop, _ := st.ListKopluginDevices(ctx, bob.ID)
	if len(kop) != 1 || kop[0].RevokedAt == nil {
		t.Fatalf("koplugin device not revoked: %+v", kop)
	}

	// The sweep needs the acting admin's own password.
	c, body := postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/credentials/revoke",
		url.Values{"csrf": {csrf}, "admin_password": {"not-my-password"}})
	if c != 200 || !strings.Contains(body, "your password is wrong") {
		t.Fatalf("revoke-all with a wrong password: %d %q", c, body)
	}
	c, _ = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/credentials/revoke",
		url.Values{"csrf": {csrf}, "admin_password": {"hunter2hunter"}})
	if c != 200 {
		t.Fatalf("revoke-all: %d", c)
	}
}

// TestAdminResetPassword covers the reset and what it does to the
// account's other credentials: sessions go, devices stay.
func TestAdminResetPassword(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	ctx := t.Context()
	if err := st.CreateAuthSession(ctx, store.AuthSession{
		ID: "bob-web", UserID: bob.ID, SHA256: "bob-web-sha", Kind: "web",
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateToken(ctx, store.Token{
		ID: "tok-1", UserID: bob.ID, DeviceID: "d1", Name: "Boox",
		Scopes: store.ScopeSet{store.ScopeSync}, SHA256: "tok-sha",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=user&user="+bob.ID)
	csrf := extractCSRF(t, body)

	// Mismatched confirmation is refused before the admin password is
	// even consulted.
	c, body := postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/password", url.Values{
		"csrf": {csrf}, "password": {"a-new-password"}, "repeat": {"different"},
		"admin_password": {"hunter2hunter"},
	})
	if c != 200 || !strings.Contains(body, "do not match") {
		t.Fatalf("mismatched repeat: %d %q", c, body)
	}

	c, _ = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/password", url.Values{
		"csrf": {csrf}, "password": {"a-new-password"}, "repeat": {"a-new-password"},
		"admin_password": {"hunter2hunter"},
	})
	if c != 200 {
		t.Fatalf("reset password: %d", c)
	}
	got, _ := st.UserByID(ctx, bob.ID)
	if ok, _ := auth.CheckPassword("a-new-password", got.Argon2Hash); !ok {
		t.Fatal("password was not changed")
	}
	if a, err := st.AuthSessionByHash(ctx, "bob-web-sha"); err != nil || a.RevokedAt == nil {
		t.Fatalf("web session survived a password reset: %+v %v", a, err)
	}
	toks, _ := st.ListTokens(ctx, bob.ID)
	if len(toks) != 1 || toks[0].RevokedAt != nil {
		t.Fatalf("a password reset revoked a device token: %+v", toks)
	}
}

// TestAdminGrantAndRevokeRole covers promotion from the panel, the
// refusal to demote yourself, and the last-admin guard reaching the
// page as a message rather than a 500.
func TestAdminGrantAndRevokeRole(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	ctx := t.Context()
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=user&user="+bob.ID)
	csrf := extractCSRF(t, body)

	c, _ := postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/admin", url.Values{
		"csrf": {csrf}, "admin": {"on"}, "admin_password": {"hunter2hunter"},
	})
	if c != 200 {
		t.Fatalf("grant admin: %d", c)
	}
	if got, _ := st.UserByID(ctx, bob.ID); !got.IsAdmin {
		t.Fatal("bob should be an administrator")
	}

	// Demoting yourself is not offered, and is refused when asked for.
	c, body = postForm(t, ts, cookie, "/ui/admin/users/u1/admin", url.Values{
		"csrf": {csrf}, "admin": {"off"}, "admin_password": {"hunter2hunter"},
	})
	if c != 200 || !strings.Contains(body, "another account") {
		t.Fatalf("self-demotion: %d %q", c, body)
	}
	if got, _ := st.UserByID(ctx, "u1"); !got.IsAdmin {
		t.Fatal("alice demoted herself")
	}

	// Demoting somebody else works, and then the last-admin guard fires.
	c, _ = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/admin", url.Values{
		"csrf": {csrf}, "admin": {"off"}, "admin_password": {"hunter2hunter"},
	})
	if c != 200 {
		t.Fatalf("revoke admin: %d", c)
	}
	if got, _ := st.UserByID(ctx, bob.ID); got.IsAdmin {
		t.Fatal("bob should have been demoted")
	}
}

// TestAdminReauthIsRateLimited is the reason the re-verification is not
// simply a password field: a verifier reachable from a stolen session
// is an online password oracle, so it burns a budget keyed on the
// acting admin regardless of where the attempts come from.
func TestAdminReauthIsRateLimited(t *testing.T) {
	ts, st := testServerCfg(t, nil, func(s *Server) {
		s.AdminReauthUserLimiter = auth.NewRateLimiter(2, time.Minute)
		s.AdminReauthIPLimiter = auth.NewRateLimiter(100, time.Minute)
	})
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=user&user="+bob.ID)
	csrf := extractCSRF(t, body)

	form := url.Values{"csrf": {csrf}, "admin_password": {"wrong"}}
	for i := range 2 {
		if c, _ := postForm(t, ts, cookie,
			"/ui/admin/users/"+bob.ID+"/credentials/revoke", form); c != 200 {
			t.Fatalf("attempt %d: %d", i, c)
		}
	}
	c, body := postForm(t, ts, cookie,
		"/ui/admin/users/"+bob.ID+"/credentials/revoke", form)
	if c != http.StatusTooManyRequests {
		t.Fatalf("third attempt: want 429, got %d", c)
	}
	if !strings.Contains(body, "too many attempts") {
		t.Fatalf("rate-limited page: %q", body)
	}
	// The budget is spent even for the correct password: a limiter that
	// only counted failures would let an attacker probe forever as long
	// as they eventually guessed right.
	c, _ = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/credentials/revoke",
		url.Values{"csrf": {csrf}, "admin_password": {"hunter2hunter"}})
	if c != http.StatusTooManyRequests {
		t.Fatalf("correct password while limited: want 429, got %d", c)
	}
}

func TestAdminReauthSpendsBothBudgetsOnEveryAttempt(t *testing.T) {
	hash, _ := auth.HashPassword("hunter2hunter")
	actor1 := &store.User{ID: "admin-1", Argon2Hash: hash}
	actor2 := &store.User{ID: "admin-2", Argon2Hash: hash}
	s := &Server{
		Cfg:                    config.Default(),
		AdminReauthUserLimiter: auth.NewRateLimiter(1, time.Minute),
		AdminReauthIPLimiter:   auth.NewRateLimiter(1, time.Minute),
	}
	request := func(addr string) *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(
			url.Values{"admin_password": {"wrong"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = addr
		_ = r.ParseForm()
		return r
	}

	if err := s.reauth(request("192.0.2.1:1000"), actor1); !errors.Is(err, errBadAdminPassword) {
		t.Fatalf("first attempt: got %v", err)
	}
	if err := s.reauth(request("192.0.2.2:1000"), actor1); !errors.Is(err, errRateLimited) {
		t.Fatalf("exhausted user budget: got %v", err)
	}
	// The second request must still have spent 192.0.2.2's IP budget even
	// though actor1's user budget was already exhausted.
	if err := s.reauth(request("192.0.2.2:1000"), actor2); !errors.Is(err, errRateLimited) {
		t.Fatalf("IP budget was skipped after user refusal: got %v", err)
	}
}

// TestAdminCreateUserFromThePanel covers the create form and the shared
// validation behind it.
func TestAdminCreateUserFromThePanel(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	csrf := extractCSRF(t, body)
	if !strings.Contains(body, `name="admin_password"`) {
		t.Fatal("account and invite forms do not ask for the administrator password")
	}

	if c, _ := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"name": {"carol"}, "password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"},
	}); c != http.StatusForbidden {
		t.Fatalf("create without CSRF: want 403, got %d", c)
	}
	for _, adminPassword := range []string{"", "wrong-password"} {
		c, body := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
			"csrf": {csrf}, "name": {"carol"}, "password": {"a-good-password"},
			"repeat": {"a-good-password"}, "admin_password": {adminPassword},
		})
		if c != http.StatusOK || !strings.Contains(body, "your password is wrong") {
			t.Fatalf("admin password %q: got %d %q", adminPassword, c, body)
		}
		if _, err := st.UserByName(t.Context(), "carol"); err == nil {
			t.Fatal("refused re-verification still created carol")
		}
	}

	c, body := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"carol"}, "password": {"short"}, "repeat": {"short"},
		"admin_password": {"hunter2hunter"},
	})
	if c != 200 || !strings.Contains(body, "at least 8 characters") {
		t.Fatalf("short password: %d %q", c, body)
	}
	c, body = postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"carol dvorak"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"},
	})
	if c != 200 || !strings.Contains(body, "may contain letters") {
		t.Fatalf("invalid name: %d %q", c, body)
	}
	c, body = postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"carol"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"},
	})
	if c != 200 || !strings.Contains(body, "Created carol") {
		t.Fatalf("create: %d %q", c, body)
	}
	if _, err := st.UserByName(t.Context(), "carol"); err != nil {
		t.Fatalf("carol was not created: %v", err)
	}
	// The name is taken now, and the refusal says so rather than 500ing.
	c, body = postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"carol"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"},
	})
	if c != 200 || !strings.Contains(body, "taken") {
		t.Fatalf("duplicate name: %d %q", c, body)
	}
}

type createUserFailStore struct{ store.Store }

func (createUserFailStore) CreateUser(context.Context, store.User) error {
	return errors.New("injected create-user failure")
}

type createInviteFailStore struct{ store.Store }

func (createInviteFailStore) CreateInvite(context.Context, store.Invite) error {
	return errors.New("injected create-invite failure")
}

func TestAdminCreationFailuresLeaveNoPartialState(t *testing.T) {
	t.Run("account store failure", func(t *testing.T) {
		ts, st := testServerCfg(t, nil, func(s *Server) {
			generousReauth(s)
			s.St = createUserFailStore{Store: s.St}
		})
		if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
			t.Fatal(err)
		}
		cookie := loginCookie(t, ts)
		_, pageBody := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
		code, body := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
			"csrf": {extractCSRF(t, pageBody)}, "name": {"carol"},
			"password": {"a-good-password"}, "repeat": {"a-good-password"},
			"admin_password": {"hunter2hunter"},
		})
		if code != http.StatusOK || !strings.Contains(body, "Could not create account") {
			t.Fatalf("store failure: %d %q", code, body)
		}
		if _, err := st.UserByName(t.Context(), "carol"); err == nil {
			t.Fatal("failed account creation left an account behind")
		}
	})

	for _, tc := range []struct {
		name string
		tune func(*Server)
	}{
		{
			name: "invite entropy failure",
			tune: func(s *Server) {
				s.newSecret = func() (string, error) { return "", errors.New("injected entropy failure") }
			},
		},
		{
			name: "invite short entropy output",
			tune: func(s *Server) {
				s.newSecret = func() (string, error) { return "short", nil }
			},
		},
		{
			name: "invite store failure",
			tune: func(s *Server) { s.St = createInviteFailStore{Store: s.St} },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, st := testServerCfg(t, nil, func(s *Server) {
				generousReauth(s)
				tc.tune(s)
			})
			if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
				t.Fatal(err)
			}
			cookie := loginCookie(t, ts)
			_, pageBody := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
			code, body := postForm(t, ts, cookie, "/ui/admin/invites", url.Values{
				"csrf": {extractCSRF(t, pageBody)}, "admin_password": {"hunter2hunter"},
			})
			if code != http.StatusOK || !strings.Contains(body, "Could not create invite") ||
				strings.Contains(body, `class="big"`) {
				t.Fatalf("failed invite creation: %d %q", code, body)
			}
			if invites, err := st.ListInvites(t.Context(), "u1"); err != nil || len(invites) != 0 {
				t.Fatalf("failed invite creation left state: %+v err=%v", invites, err)
			}
		})
	}
}

func TestAdminCreationPathsEnforceBothLimiters(t *testing.T) {
	for _, action := range []string{"account", "invite"} {
		for _, limited := range []string{"user", "ip"} {
			t.Run(action+"/"+limited, func(t *testing.T) {
				ts, st := testServerCfg(t, nil, func(s *Server) {
					userLimit, ipLimit := 100, 100
					if limited == "user" {
						userLimit = 1
					} else {
						ipLimit = 1
					}
					s.AdminReauthUserLimiter = auth.NewRateLimiter(userLimit, time.Minute)
					s.AdminReauthIPLimiter = auth.NewRateLimiter(ipLimit, time.Minute)
				})
				if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
					t.Fatal(err)
				}
				cookie := loginCookie(t, ts)
				_, pageBody := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
				csrf := extractCSRF(t, pageBody)
				path := "/ui/admin/invites"
				form := url.Values{"csrf": {csrf}}
				if action == "account" {
					path = "/ui/admin/users"
					form.Set("name", "blocked-user")
					form.Set("password", "a-good-password")
					form.Set("repeat", "a-good-password")
				}
				form.Set("admin_password", "wrong")
				if code, _ := postForm(t, ts, cookie, path, form); code != http.StatusOK {
					t.Fatalf("first attempt: got %d", code)
				}
				form.Set("admin_password", "hunter2hunter")
				if code, body := postForm(t, ts, cookie, path, form); code != http.StatusTooManyRequests ||
					!strings.Contains(body, "too many attempts") {
					t.Fatalf("limited attempt: got %d %q", code, body)
				}
				if _, err := st.UserByName(t.Context(), "blocked-user"); err == nil {
					t.Fatal("rate-limited request created an account")
				}
				if invites, err := st.ListInvites(t.Context(), "u1"); err != nil || len(invites) != 0 {
					t.Fatalf("rate-limited request created an invite: %+v err=%v", invites, err)
				}
			})
		}
	}
}

func TestAdminCreationAuditDoesNotLogSecrets(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	ts, st := testServerCfg(t, nil, generousReauth)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, pageBody := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	csrf := extractCSRF(t, pageBody)
	newPassword, wrongAdminPassword := "new-user-secret", "wrong-admin-secret"
	postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"carol"}, "password": {newPassword},
		"repeat": {newPassword}, "admin_password": {wrongAdminPassword},
	})
	_, inviteBody := postForm(t, ts, cookie, "/ui/admin/invites", url.Values{
		"csrf": {csrf}, "admin_password": {"hunter2hunter"},
	})
	inviteCode := secretFromPage(t, inviteBody)
	got := logs.String()
	for _, want := range []string{`"actor_id":"u1"`, `"action":"create-user"`, `"action":"create-invite"`} {
		if !strings.Contains(got, want) {
			t.Errorf("audit log is missing %s: %s", want, got)
		}
	}
	for _, secret := range []string{newPassword, wrongAdminPassword, inviteCode, auth.HashSecret(inviteCode)} {
		if strings.Contains(got, secret) {
			t.Errorf("audit log leaked a secret: %s", got)
		}
	}
}

// TestAdminUserPageMissingUserIsNotFound pins a regression: visiting the
// per-user Settings page for a user id that does not exist (a stale
// bookmark, a typo, or a deleted account) must answer 404 without
// leaking the store's internal error text, not a 500.
func TestAdminUserPageMissingUserIsNotFound(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/settings?section=admin&view=user&user=does-not-exist")
	if code != http.StatusNotFound {
		t.Fatalf("missing user page: got %d, want %d", code, http.StatusNotFound)
	}
	if strings.Contains(body, "store:") {
		t.Fatalf("response leaked internal store error text: %q", body)
	}
}

// TestAdminUserPagination covers the cursor the list pages with.
func TestAdminUserPagination(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	for i := range adminUsersPerPage + 2 {
		mkTarget(t, st, "user"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	if !strings.Contains(body, "Next page") {
		t.Fatal("a list longer than one page offers no next page")
	}
}

// TestAdminDisableAccount walks phase 6 through the panel: disabling
// stops every way in and revokes the account's sessions, enabling
// brings the devices back but not the sessions, and an administrator
// cannot lock the instance by stopping the last one.
func TestAdminDisableAccount(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	if err := st.CreateToken(ctx, store.Token{
		ID: "tok-1", UserID: bob.ID, DeviceID: "d1", Name: "Boox",
		Scopes: store.ScopeSet{store.ScopeSync}, SHA256: "tok-sha",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAuthSession(ctx, store.AuthSession{
		ID: "bob-sess", UserID: bob.ID, SHA256: "bob-sess-sha", Kind: "web",
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=user&user="+bob.ID)
	csrf := extractCSRF(t, body)
	if !strings.Contains(body, "Disable this account") {
		t.Fatal("per-user page does not offer disabling")
	}

	// It is behind the admin's own password.
	if _, b := postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/disabled", url.Values{
		"csrf": {csrf}, "disabled": {"on"}, "admin_password": {"wrong-password"},
	}); !strings.Contains(b, "your password is wrong") {
		t.Fatalf("disable without the password: %s", b)
	}
	if u, _ := st.UserByID(ctx, bob.ID); !u.Enabled() {
		t.Fatal("a refused re-verification still disabled the account")
	}

	_, body = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/disabled", url.Values{
		"csrf": {csrf}, "disabled": {"on"}, "admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "is disabled") {
		t.Fatalf("disable: %s", body)
	}
	if u, _ := st.UserByID(ctx, bob.ID); u.Enabled() {
		t.Fatal("the account is still enabled")
	}
	if _, err := st.AuthSessionByHash(ctx, "bob-sess-sha"); err == nil {
		t.Fatal("a disabled account's session still authenticates")
	}
	if !strings.Contains(body, "Enable this account") {
		t.Fatal("the page does not offer turning it back on")
	}

	_, body = postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/disabled", url.Values{
		"csrf": {csrf}, "disabled": {"off"}, "admin_password": {"hunter2hunter"},
	})
	if !strings.Contains(body, "is enabled again") {
		t.Fatalf("enable: %s", body)
	}
	if u, _ := st.UserByID(ctx, bob.ID); !u.Enabled() {
		t.Fatal("the account did not come back")
	}
	// The session stays revoked; the token never was.
	if _, err := st.AuthSessionByHash(ctx, "bob-sess-sha"); err == nil {
		if sess, _ := st.AuthSessionByHash(ctx, "bob-sess-sha"); sess.RevokedAt == nil {
			t.Fatal("re-enabling resurrected a revoked session")
		}
	}

	// The last enabled administrator cannot be stopped — not even by
	// themselves, which the page refuses before the store has to.
	if _, b := postForm(t, ts, cookie, "/ui/admin/users/u1/disabled", url.Values{
		"csrf": {csrf}, "disabled": {"on"}, "admin_password": {"hunter2hunter"},
	}); !strings.Contains(b, "from another administrator") {
		t.Fatalf("self-disable: %s", b)
	}
	// And an administrator who is disabled loses the panel in the same
	// instant, because disabling revoked their sessions too.
	if err := st.SetUserAdmin(ctx, bob.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserDisabled(ctx, "u1", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _ := postForm(t, ts, cookie, "/ui/admin/users/"+bob.ID+"/disabled", url.Values{
		"csrf": {csrf}, "disabled": {"on"}, "admin_password": {"hunter2hunter"},
	}); code != http.StatusSeeOther {
		t.Fatalf("a disabled admin still reached the panel: %d", code)
	}
	if resp, _ := get(t, ts, cookie, "/ui/settings?section=admin&view=user&user="+bob.ID); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("a disabled admin still reads the panel: %d", resp.StatusCode)
	}
	// The store is the guard of last resort for the last administrator.
	if err := st.SetUserDisabled(ctx, bob.ID, true, time.Now()); err == nil {
		t.Fatal("the last enabled administrator was disabled")
	}
}

// TestAdminUserPageHidesRevokedCredentials: the per-user page lists the
// credentials the account can still use. A revoked one is history, so it
// is not a row with a "Revoked" column beside it.
func TestAdminUserPageHidesRevokedCredentials(t *testing.T) {
	ts, st := testServer(t)
	if err := st.SetUserAdmin(t.Context(), "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	ctx := t.Context()
	now := time.Now()
	for _, tok := range []store.Token{
		{ID: "live-tok", UserID: bob.ID, DeviceID: "live-device-id", Name: "Boox Palma",
			Scopes: store.ScopeSet{store.ScopeSync}, SHA256: "live-sha", CreatedAt: now},
		{ID: "dead-tok", UserID: bob.ID, DeviceID: "retired-device-id", Name: "Retired Palma",
			Scopes: store.ScopeSet{store.ScopeSync}, SHA256: "dead-sha", CreatedAt: now},
	} {
		if err := st.CreateToken(ctx, tok); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RevokeToken(ctx, bob.ID, "dead-tok"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct{ slot, label string }{{"slot-live", "kobo"}, {"slot-dead", "old kobo"}} {
		if err := st.CreateKosyncDevice(ctx, store.KosyncDevice{
			UserID: bob.ID, DeviceSlot: d.slot, KeySHA256: d.slot + "-sha", Label: d.label,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RevokeKosyncDevice(ctx, bob.ID, "slot-dead"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct{ id, label string }{{"kop-live", "clara"}, {"kop-dead", "old clara"}} {
		if err := st.CreateKopluginDevice(ctx, store.KopluginDevice{
			ID: d.id, UserID: bob.ID, TokenSHA256: d.id + "-sha", Label: d.label,
			DeviceID: "dev:" + d.label, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RevokeKopluginDevice(ctx, bob.ID, "kop-dead"); err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/settings?section=admin&view=user&user="+bob.ID)
	if code != 200 {
		t.Fatalf("user page: %d", code)
	}
	for _, want := range []string{"Boox Palma", "slot-live", "clara"} {
		if !strings.Contains(body, want) {
			t.Errorf("user page is missing the active credential %q", want)
		}
	}
	for _, gone := range []string{"Retired Palma", "retired-device-id", "slot-dead", "old kobo", "old clara"} {
		if strings.Contains(body, gone) {
			t.Errorf("user page still lists the revoked %q", gone)
		}
	}
	if strings.Contains(body, ">Revoked<") {
		t.Error("the per-user credential tables still have a Revoked column")
	}
}

// TestAdminCreateUserGrantsChosenFolders covers the folder checkboxes on
// the create form: every watched folder is ticked on a first visit,
// because a new account starts with no catalog at all and the unticking
// is the deliberate act.
func TestAdminCreateUserGrantsChosenFolders(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	keep := store.Folder{
		ID: "shelf-kept", Name: "Kept Shelf", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	cleared := store.Folder{
		ID: "shelf-cleared", Name: "Cleared Shelf", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	for _, f := range []store.Folder{keep, cleared} {
		if err := st.CreateFolder(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	for _, want := range []string{
		"Folder access", "Kept Shelf", "Cleared Shelf",
		`value="` + keep.ID + `" checked`, `value="` + cleared.ID + `" checked`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the create form is missing %q", want)
		}
	}
	csrf := extractCSRF(t, body)

	resp, _ := postNoFollow(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"carol"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"}, "folders": {keep.ID},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	carol, err := st.UserByName(ctx, "carol")
	if err != nil {
		t.Fatalf("carol was not created: %v", err)
	}
	location := resp.Header.Get("Location")
	// The destination is the new account's own page, named relative to
	// the URL the form posted to: that is where the rest of its setup
	// lives, and a browser behind a path prefix has to stay under it.
	loc, err := url.Parse(location)
	if err != nil {
		t.Fatalf("create redirected to %q, which does not parse: %v", location, err)
	}
	if loc.Path != "../settings" {
		t.Fatalf("create redirected to %q, want the settings page", location)
	}
	if got := loc.Query().Get("section"); got != settingsAdmin {
		t.Fatalf("create redirected to section %q, want %q", got, settingsAdmin)
	}
	if got := loc.Query().Get("view"); got != settingsAdminUser {
		t.Fatalf("create redirected to view %q, want %q", got, settingsAdminUser)
	}
	if got := loc.Query().Get("user"); got != carol.ID {
		t.Fatalf("create redirected to user %q, want %q", got, carol.ID)
	}
	code, landed := page(t, ts, cookie, "/ui/"+strings.TrimPrefix(location, "../"))
	if code != http.StatusOK {
		t.Fatalf("the page after the redirect: %d", code)
	}
	body = landed
	if !strings.Contains(body, "Created carol") ||
		!strings.Contains(body, "Folder access") ||
		!strings.Contains(body, keep.ID) {
		t.Fatalf("the browser did not land on the new account's page: %s", body)
	}
	if _, err := st.FolderByID(ctx, carol.ID, keep.ID); err != nil {
		t.Fatalf("the ticked folder was not granted: %v", err)
	}
	if _, err := st.FolderByID(ctx, carol.ID, cleared.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the cleared folder was granted anyway: %v", err)
	}
	if carol.IsAdmin {
		t.Fatal("creating an account granted administrator rights")
	}
}

func TestAdminCreateUserGrantsAllOrNoFolders(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	folders := []store.Folder{
		{ID: "all-a", Name: "All A", RootPath: t.TempDir(),
			Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "all-b", Name: "All B", RootPath: t.TempDir(),
			Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	for _, folder := range folders {
		if err := st.CreateFolder(ctx, folder); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	csrf := extractCSRF(t, body)

	code, body := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"all-granted"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"},
		"folders":        {folders[0].ID, folders[1].ID},
	})
	if code != http.StatusOK || !strings.Contains(body, "with access to 2 folders") {
		t.Fatalf("grant all folders: %d %q", code, body)
	}
	allGranted, err := st.UserByName(ctx, "all-granted")
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range folders {
		if _, err := st.FolderByID(ctx, allGranted.ID, folder.ID); err != nil {
			t.Fatalf("all-granted missing %s: %v", folder.ID, err)
		}
	}

	_, body = page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	csrf = extractCSRF(t, body)
	code, body = postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"none-granted"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"},
	})
	if code != http.StatusOK || !strings.Contains(body, "Created none-granted") {
		t.Fatalf("grant no folders: %d %q", code, body)
	}
	noneGranted, err := st.UserByName(ctx, "none-granted")
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range folders {
		if _, err := st.FolderByID(ctx, noneGranted.ID, folder.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("none-granted unexpectedly has %s: %v", folder.ID, err)
		}
	}
}

func TestAdminCreateUserWhenNoFoldersAreWatched(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	if !strings.Contains(body, "No folders are watched, so this account starts with none.") ||
		strings.Contains(body, `name="folders"`) {
		t.Fatalf("the create form did not show the no-folders state: %s", body)
	}
	csrf := extractCSRF(t, body)
	code, _ := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"no-folders"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"},
	})
	if code != http.StatusOK {
		t.Fatalf("create with no watched folders: %d", code)
	}
	if _, err := st.UserByName(ctx, "no-folders"); err != nil {
		t.Fatalf("the account was not created: %v", err)
	}
}

// TestAdminCreateUserRefusalKeepsTheForm pins what a validation refusal
// hands back: the name and the folder ticks, and never a password.
func TestAdminCreateUserRefusalKeepsTheForm(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	for _, f := range []store.Folder{
		{ID: "shelf-a", Name: "Shelf A", RootPath: t.TempDir(),
			Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "shelf-b", Name: "Shelf B", RootPath: t.TempDir(),
			Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	} {
		if err := st.CreateFolder(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	code, body := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {extractCSRF(t, body)}, "name": {"carol dvorak"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"}, "folders": {"shelf-a"},
	})
	if code != http.StatusOK || !strings.Contains(body, "may contain letters") {
		t.Fatalf("invalid name: %d %q", code, body)
	}
	if !strings.Contains(body, `value="carol dvorak"`) {
		t.Fatalf("the typed name did not survive the refusal: %s", body)
	}
	if !strings.Contains(body, `value="shelf-a" checked`) {
		t.Fatal("the ticked folder did not survive the refusal")
	}
	if strings.Contains(body, `value="shelf-b" checked`) {
		t.Fatal("a cleared folder came back ticked after the refusal")
	}
	if strings.Contains(body, "a-good-password") {
		t.Fatal("the refusal refilled a password field")
	}
}

// TestAdminCreateUserChecksEveryFolderBeforeCreating: a grant set naming
// a folder this server does not watch is refused before the account row
// is written, so a stale page cannot leave a half-granted account behind.
func TestAdminCreateUserChecksEveryFolderBeforeCreating(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	code, body := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {extractCSRF(t, body)}, "name": {"carol"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"}, "folders": {"gone-with-the-shelf"},
	})
	if code != http.StatusOK || !strings.Contains(body, "no longer watched") {
		t.Fatalf("unknown folder: %d %q", code, body)
	}
	if _, err := st.UserByName(ctx, "carol"); err == nil {
		t.Fatal("a refused folder grant still created the account")
	}
}

// TestAdminCreateUserRateLimitedRefusalKeepsTheSelection: the reauth
// budget's 429 renders in place, because the status is the answer, and it
// has to hand back the same preserved state a validation refusal does.
// Otherwise the operator's retry after the wait quietly grants every
// folder they had deliberately cleared.
func TestAdminCreateUserRateLimitedRefusalKeepsTheSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		keep []string
	}{
		{name: "a subset", keep: []string{"shelf-a"}},
		{name: "none", keep: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, st := testServerCfg(t, nil, func(s *Server) {
				s.AdminReauthUserLimiter = auth.NewRateLimiter(1, time.Minute)
				s.AdminReauthIPLimiter = auth.NewRateLimiter(100, time.Minute)
			})
			ctx := t.Context()
			if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
				t.Fatal(err)
			}
			folders := []store.Folder{
				{ID: "shelf-a", Name: "Shelf A", RootPath: t.TempDir(),
					Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now()},
				{ID: "shelf-b", Name: "Shelf B", RootPath: t.TempDir(),
					Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now()},
			}
			for _, f := range folders {
				if err := st.CreateFolder(ctx, f); err != nil {
					t.Fatal(err)
				}
			}
			cookie := loginCookie(t, ts)
			_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
			form := url.Values{
				"csrf": {extractCSRF(t, body)}, "name": {"carol"},
				"password": {"a-good-password"}, "repeat": {"a-good-password"},
			}
			for _, id := range tc.keep {
				form.Add("folders", id)
			}
			// The first attempt is what spends the budget.
			form.Set("admin_password", "wrong-password")
			if code, _ := postForm(t, ts, cookie, "/ui/admin/users", form); code != http.StatusOK {
				t.Fatalf("the attempt that spends the budget: %d", code)
			}
			form.Set("admin_password", "hunter2hunter")
			resp, body := postNoFollow(t, ts, cookie, "/ui/admin/users", form)
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("limited attempt: want 429, got %d", resp.StatusCode)
			}
			if got := resp.Header.Get("Retry-After"); got != "60" {
				t.Fatalf("limited attempt: Retry-After %q", got)
			}
			if !strings.Contains(body, "too many attempts") {
				t.Fatalf("the 429 does not say why: %s", body)
			}
			if !strings.Contains(body, `value="carol"`) {
				t.Fatal("the 429 lost the name that was typed")
			}
			for _, f := range folders {
				want := slices.Contains(tc.keep, f.ID)
				got := strings.Contains(body, `value="`+f.ID+`" checked`)
				if got != want {
					t.Fatalf("folder %s: ticked %v after the 429, want %v", f.ID, got, want)
				}
			}
			if strings.Contains(body, "a-good-password") {
				t.Fatal("the 429 refilled a password field")
			}
			if _, err := st.UserByName(ctx, "carol"); err == nil {
				t.Fatal("a rate-limited attempt created the account")
			}
		})
	}
}

// uuidName is a folder id of the shape a real catalog holds: 500 of them
// are what made an id-list encoding of a refusal state 19KB.
func uuidName(i int) string {
	return fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
}

// TestAdminCreateUserRefusalFitsAHeader: the form may legitimately hold
// adminUserFoldersMax folders, and what a refusal hands back travels in a
// Location header. As a list of ids a half-selection of 500 UUID folders
// is several kilobytes — past what the documented nginx deployment
// accepts, so a plain refusal would become a proxy error. The state is
// positional instead, and it still restores the selection exactly.
func TestAdminCreateUserRefusalFitsAHeader(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ids := make([]string, adminUserFoldersMax)
	for i := range ids {
		ids[i] = uuidName(i)
		// Named in order, because the folder table lists by name and the
		// restored ticks are positional.
		if err := st.CreateFolder(ctx, store.Folder{
			ID: ids[i], Name: fmt.Sprintf("Shelf %03d", i),
			RootPath: filepath.Join(root, ids[i]),
			Kind:     store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginCookie(t, ts)

	refuse := func(t *testing.T, ticked []string) string {
		t.Helper()
		_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
		form := url.Values{
			"csrf": {extractCSRF(t, body)}, "name": {"carol dvorak"},
			"password": {"a-good-password"}, "repeat": {"a-good-password"},
			"admin_password": {"hunter2hunter"},
		}
		for _, id := range ticked {
			form.Add("folders", id)
		}
		resp, _ := postNoFollow(t, ts, cookie, "/ui/admin/users", form)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("a validation refusal: want 303, got %d", resp.StatusCode)
		}
		location := resp.Header.Get("Location")
		// A default nginx allows 8KB of request line and proxy header;
		// leaving it an order of magnitude of room is the point.
		if len(location) > 1024 {
			t.Fatalf("the refusal's Location is %d bytes: %s", len(location), location)
		}
		t.Logf("the refusal's Location is %d bytes with %d folders", len(location), len(ids))
		code, body := page(t, ts, cookie, "/ui/"+strings.TrimPrefix(location, "../"))
		if code != http.StatusOK {
			t.Fatalf("the page at the end of the refusal: %d", code)
		}
		if !strings.Contains(body, `value="carol dvorak"`) {
			t.Fatal("the refusal lost the name that was typed")
		}
		return body
	}

	t.Run("half the folders cleared", func(t *testing.T) {
		ticked := make([]string, 0, len(ids)/2)
		for i, id := range ids {
			if i%2 == 0 {
				ticked = append(ticked, id)
			}
		}
		body := refuse(t, ticked)
		for i, id := range ids {
			want := i%2 == 0
			got := strings.Contains(body, `value="`+id+`" checked`)
			if got != want {
				t.Fatalf("folder %d: ticked %v after the refusal, want %v", i, got, want)
			}
		}
	})
	t.Run("every folder cleared", func(t *testing.T) {
		body := refuse(t, nil)
		if got := strings.Count(body, `name="folders"`); got != adminUserFoldersMax {
			t.Fatalf("the restored form has %d folder boxes, want %d", got, adminUserFoldersMax)
		}
		for _, id := range ids {
			if strings.Contains(body, `value="`+id+`" checked`) {
				t.Fatalf("cleared folder %s came back ticked", id)
			}
		}
	})
}

// TestAdminCreateUserDropsASelectionTakenAgainstAnotherList: the ticks
// are positions, so they are only replayed onto the folder list they were
// taken on. A folder appearing between the refusal and the reload must
// not shift them onto folders the operator cleared.
func TestAdminCreateUserDropsASelectionTakenAgainstAnotherList(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	for _, f := range []store.Folder{
		{ID: "shelf-a", Name: "Shelf A", RootPath: t.TempDir(),
			Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "shelf-b", Name: "Shelf B", RootPath: t.TempDir(),
			Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	} {
		if err := st.CreateFolder(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	resp, _ := postNoFollow(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {extractCSRF(t, body)}, "name": {"carol dvorak"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"}, "folders": {"shelf-a"},
	})
	location := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || location == "" {
		t.Fatalf("the refusal: %d %q", resp.StatusCode, location)
	}
	// A folder appears between the refusal and the reload, which shifts
	// every tick after it.
	if err := st.CreateFolder(ctx, store.Folder{
		ID: "shelf-0", Name: "Shelf 0", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	_, body = page(t, ts, cookie, "/ui/"+strings.TrimPrefix(location, "../"))
	if !strings.Contains(body, `value="carol dvorak"`) {
		t.Fatal("the name is not a positional claim and should have survived")
	}
	if !strings.Contains(body, "The folders changed while this form was open") {
		t.Fatal("a selection that no longer applies was not explained")
	}
	for _, id := range []string{"shelf-a", "shelf-b"} {
		if strings.Contains(body, `value="`+id+`" checked`) {
			t.Fatalf("a stale selection ticked %s: nothing is safer than a guess", id)
		}
	}
}

// TestAdminCreateUserDeduplicatesFolderSubmissions: both stores insert a
// grant set once per folder, so a body naming one folder twice grants one
// folder — and must be counted as one.
func TestAdminCreateUserDeduplicatesFolderSubmissions(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	folder := store.Folder{
		ID: "shelf-one", Name: "Shelf One", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := st.CreateFolder(ctx, folder); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	code, body := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {extractCSRF(t, body)}, "name": {"carol"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"},
		"folders":        {folder.ID, folder.ID},
	})
	if code != http.StatusOK || !strings.Contains(body, "with access to 1 folder") {
		t.Fatalf("one folder named twice: %d %q", code, body)
	}
	carol, err := st.UserByName(ctx, "carol")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.FolderByID(ctx, carol.ID, folder.ID); err != nil {
		t.Fatalf("the folder was not granted: %v", err)
	}
}

// manyFoldersStore reports more folders than the create form may replace
// in one go, without writing 500 rows to find out.
type manyFoldersStore struct {
	store.Store
	folders []store.Folder
}

func (m manyFoldersStore) ListFolders(
	context.Context, string, string, int,
) ([]store.Folder, error) {
	return m.folders, nil
}

func tooManyFolders() []store.Folder {
	out := make([]store.Folder, 0, adminUserFoldersMax+1)
	for range adminUserFoldersMax + 1 {
		out = append(out, store.Folder{ID: store.NewID(), Name: "Shelf"})
	}
	return out
}

// TestAdminCreateUserWithTooManyFolders: past the bound the form offers
// no checkboxes, a submitted selection is refused rather than silently
// dropped, and an account created without one says how to grant folders.
func TestAdminCreateUserWithTooManyFolders(t *testing.T) {
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.St = manyFoldersStore{Store: s.St, folders: tooManyFolders()}
	})
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	if !strings.Contains(body, "too many folders") ||
		strings.Contains(body, `name="folders"`) {
		t.Fatalf("the create form offered a grant set it cannot submit: %s", body)
	}
	csrf := extractCSRF(t, body)

	code, body := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"dropped"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"}, "folders": {"some-folder"},
	})
	if code != http.StatusOK || !strings.Contains(body, "too many folders") {
		t.Fatalf("a selection the form cannot replace: %d %q", code, body)
	}
	// The name is handed back even though no selection accompanies it.
	if !strings.Contains(body, `value="dropped"`) {
		t.Fatal("the refusal lost the name that was typed")
	}
	if _, err := st.UserByName(ctx, "dropped"); err == nil {
		t.Fatal("a refused selection still created the account")
	}

	code, body = postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {csrf}, "name": {"ungranted"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"},
	})
	if code != http.StatusOK || !strings.Contains(body, "assign-all-folders") {
		t.Fatalf("an account without folder access: %d %q", code, body)
	}
	if _, err := st.UserByName(ctx, "ungranted"); err != nil {
		t.Fatalf("the account was not created: %v", err)
	}
}

type grantFailStore struct{ store.Store }

func (grantFailStore) ReplaceUserFolders(context.Context, string, []string) error {
	return errors.New("injected grant failure")
}

// TestAdminCreateUserReportsGrantsThatDidNotApply: the insert and the
// grants are two transactions. If the second fails the page must not
// claim the account was set up as asked — it says what happened and
// leaves the browser on the page that can fix it.
func TestAdminCreateUserReportsGrantsThatDidNotApply(t *testing.T) {
	ts, st := testServerCfg(t, nil, func(s *Server) {
		generousReauth(s)
		s.St = grantFailStore{Store: s.St}
	})
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	folder := store.Folder{
		ID: "shelf-one", Name: "Shelf One", RootPath: t.TempDir(),
		Kind: store.FolderPlain, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := st.CreateFolder(ctx, folder); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	code, body := postForm(t, ts, cookie, "/ui/admin/users", url.Values{
		"csrf": {extractCSRF(t, body)}, "name": {"carol"},
		"password": {"a-good-password"}, "repeat": {"a-good-password"},
		"admin_password": {"hunter2hunter"}, "folders": {folder.ID},
	})
	if code != http.StatusOK {
		t.Fatalf("create with a failing grant: %d", code)
	}
	if !strings.Contains(body, "could not be set") ||
		strings.Contains(body, "with access to") {
		t.Fatalf("a failed grant was reported as success: %s", body)
	}
	carol, err := st.UserByName(ctx, "carol")
	if err != nil {
		t.Fatalf("the account should still exist: %v", err)
	}
	if _, err := st.FolderByID(ctx, carol.ID, folder.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the failed grant left access behind: %v", err)
	}
}

// TestAdminUserPageGroupsDevicesAndSecurity: the per-user page is two
// cards, not seven. Devices and integrations holds every credential kind
// with its add form behind a native disclosure; Account & security holds
// the four disruptive controls, the last of them styled as one.
func TestAdminUserPageGroupsDevicesAndSecurity(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	if err := st.CreateToken(ctx, store.Token{
		ID: "tok-1", UserID: bob.ID, DeviceID: "d1", Name: "Boox Palma",
		Scopes: store.ScopeSet{store.ScopeSync, store.ScopeLibraryRead},
		SHA256: "tok-sha", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateKosyncDevice(ctx, store.KosyncDevice{
		UserID: bob.ID, DeviceSlot: "slot-1", KeySHA256: "kosync-sha", Label: "kobo",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateKopluginDevice(ctx, store.KopluginDevice{
		ID: "kop-1", UserID: bob.ID, TokenSHA256: "kop-sha", Label: "clara",
		DeviceID: "dev-1", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	_, body := page(t, ts, loginCookie(t, ts),
		"/ui/settings?section=admin&view=user&user="+bob.ID)
	for _, want := range []string{
		"Devices &amp; integrations", "API tokens", "KOReader (kosync)",
		"Statistics plugin devices", "Account &amp; security",
		"Mint an API token", "Pair a KOReader", "Add a statistics plugin device",
		"Reset the password", "Administrator rights", "Account status",
		"Revoke every credential", "danger-zone",
		// Every active entry survives the regroup, and the token's scopes
		// render as badges rather than one string.
		"Boox Palma", "slot-1", "clara", `<span class="tag">sync</span>`,
		"Revoke everything",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the per-user page is missing %q", want)
		}
	}
	if got := strings.Count(body, `<details class="setup-guide`); got < 7 {
		t.Fatalf("the page has %d disclosures, want every form behind one", got)
	}
	if got := nestedFormDepth(t, body); got != 0 {
		t.Fatalf("the per-user page nests %d form(s) inside another form", got)
	}
	if strings.Contains(body, "<form method=\"post\" action=\"/ui") {
		t.Fatal("a form action is absolute rather than relative to the proxy prefix")
	}
}

// TestAdminPagesNestNoForms: a form inside a form is dropped by the
// browser, and its fields with it. The users page carries two forms of
// its own plus one per invite, so it is the page where nesting would
// creep in.
func TestAdminPagesNestNoForms(t *testing.T) {
	ts, st := testServer(t)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	code, _ := postForm(t, ts, cookie, "/ui/admin/invites", url.Values{
		"csrf": {extractCSRF(t, body)}, "admin_password": {"hunter2hunter"},
	})
	if code != http.StatusOK {
		t.Fatalf("create an invite: %d", code)
	}
	_, body = page(t, ts, cookie, "/ui/settings?section=admin&view=users")
	if got := strings.Count(body, "<form "); got < 3 {
		t.Fatalf("the users page has %d forms, want the create, invite and revoke forms", got)
	}
	if got := nestedFormDepth(t, body); got != 0 {
		t.Fatalf("the users page nests %d form(s) inside another form", got)
	}
}

// TestAdminSecretResponseIsNotStored: a page carrying a once-shown
// credential must not be cached or kept in a back-forward cache.
func TestAdminSecretResponseIsNotStored(t *testing.T) {
	ts, st := testServerCfg(t, nil, generousReauth)
	ctx := t.Context()
	if err := st.SetUserAdmin(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	bob := mkTarget(t, st, "bob")
	cookie := loginCookie(t, ts)
	_, body := page(t, ts, cookie, "/ui/settings?section=admin&view=user&user="+bob.ID)
	csrf := extractCSRF(t, body)

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/ui/admin/users/"+bob.ID+"/tokens",
		strings.NewReader(url.Values{
			"csrf": {csrf}, "name": {"Boox"}, "scopes": {"sync"},
			"admin_password": {"hunter2hunter"},
		}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mint: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("a one-time secret is cacheable: Cache-Control %q", got)
	}
	if !strings.Contains(string(raw), `id="flash-secret-value"`) {
		t.Fatalf("the secret is not rendered in place: %s", raw)
	}
	if strings.Contains(resp.Header.Get("Location"), "secret") {
		t.Fatal("the secret travelled in a redirect")
	}
}
