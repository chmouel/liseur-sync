package webui

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

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
