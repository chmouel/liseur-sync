package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// LoginTTL is the lifetime of the short-lived auth credential returned
// by login (usable only for token management).
const LoginTTL = time.Hour

// ReaderTokenTTL is the lifetime of a browser reader's API credential.
// The reader refreshes by asking for another one with the session cookie
// it already holds, so there is no refresh token to steal and expiry is
// the only revocation the reader has to implement.
const ReaderTokenTTL = time.Hour

// ReaderTokenName marks tokens minted for the browser reader. It is how
// a reader token is recognised again, which is what keeps one browser to
// one device identity in the op log.
const ReaderTokenName = "Web reader"

// readerScopes is the whole capability of a browser reader: read the
// catalog, sync positions. It is deliberately not derived from the
// user's other tokens, so a reader credential can never carry
// library-manage or admin however privileged its owner is.
var readerScopes = store.ScopeSet{store.ScopeSync, store.ScopeLibraryRead}

// ErrAdminGrantRequiresAdmin prevents login credentials from bootstrapping
// instance-wide privileges.
var ErrAdminGrantRequiresAdmin = errors.New("admin scope requires an existing admin token")

// Service wires auth against a store.
type Service struct {
	St  store.Store
	Now func() time.Time // test hook
}

func NewService(st store.Store) *Service {
	return &Service{St: st, Now: time.Now}
}

// Login verifies username+password and, on success, issues a
// short-lived auth credential. Returns the plaintext secret once.
func (s *Service) Login(ctx context.Context, username, password string) (secret string, err error) {
	u, err := s.St.UserByName(ctx, username)
	if err != nil {
		CheckDummyPassword(password)
		return "", errors.New("invalid credentials")
	}
	ok, err := CheckPassword(password, u.Argon2Hash)
	if err != nil || !ok {
		return "", errors.New("invalid credentials")
	}
	// The one explicit check (ADR-0013): every other way in resolves
	// through a credential lookup that joins against users, but a login
	// starts from UserByName, which must keep returning disabled
	// accounts so the admin panel can render them. The password is
	// verified first so that a disabled account is not an oracle for
	// which names exist.
	if !u.Enabled() {
		return "", errors.New("invalid credentials")
	}
	return s.issueAuthSession(ctx, u.ID, "login", "")
}

func (s *Service) issueAuthSession(ctx context.Context, userID, kind, csrfHash string) (string, error) {
	secret, err := NewSecret()
	if err != nil {
		return "", err
	}
	id, err := NewSecret()
	if err != nil {
		return "", err
	}
	now := s.Now()
	err = s.St.CreateAuthSession(ctx, store.AuthSession{
		ID:        id,
		UserID:    userID,
		SHA256:    HashSecret(secret),
		Kind:      kind,
		CSRFHash:  csrfHash,
		CreatedAt: now,
		ExpiresAt: now.Add(LoginTTL),
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

// AuthenticateLogin validates a login credential, returning the user ID.
func (s *Service) AuthenticateLogin(ctx context.Context, secret string) (string, error) {
	a, err := s.St.AuthSessionByHash(ctx, HashSecret(secret))
	if err != nil || a.Kind != "login" || a.RevokedAt != nil || s.Now().After(a.ExpiresAt) {
		return "", errors.New("invalid auth credential")
	}
	return a.UserID, nil
}

// CreateToken mints a per-device token for a user authenticated via the
// login credential. Returns the plaintext secret once.
func (s *Service) CreateToken(ctx context.Context, loginSecret, name string, scopes store.ScopeSet, expiresAt *time.Time) (plaintext string, tok store.Token, err error) {
	userID, err := s.AuthenticateLogin(ctx, loginSecret)
	if err != nil {
		return "", tok, err
	}
	if err := s.CheckScopeGrant(ctx, userID, scopes); err != nil {
		return "", tok, err
	}
	return s.MintToken(ctx, userID, name, scopes, expiresAt)
}

// MintToken creates a token for a known user (admin CLI path).
func (s *Service) MintToken(ctx context.Context, userID, name string, requested store.ScopeSet, expiresAt *time.Time) (string, store.Token, error) {
	return s.mintToken(ctx, userID, name, requested, expiresAt, "")
}

// MintReaderToken issues the browser reader's API credential: fixed
// scopes, an hour's life, and the caller's existing web device identity
// if they have read in a browser before.
//
// The device identity is reused rather than freshly minted because op
// log heads are per work *and* device. A device per tab, or per hour,
// would turn one person reading one book into several competing heads,
// and "where did I stop" would depend on which window asked.
//
// A live predecessor is left strictly alone. Revoking one here would let
// two open tabs invalidate each other's credential in a loop, each
// re-minting in response to the other's failure.
//
// A dead one is deleted rather than revoked. The reader asks for a
// credential on every open and again every hour, so anything kept
// accumulates for as long as somebody reads; and there is nothing to
// keep, since nobody asked for these and a revoked row only means
// something when a person cut a device off. The newest per device
// survives whatever its state, because it carries the device id the
// next mint inherits — delete that and a browser left closed overnight
// comes back as a stranger to the op log.
func (s *Service) MintReaderToken(ctx context.Context, userID string) (string, store.Token, error) {
	existing, err := s.St.ListTokens(ctx, userID)
	if err != nil {
		return "", store.Token{}, fmt.Errorf("list tokens: %w", err)
	}
	now := s.Now()

	newestPerDevice := map[string]store.Token{}
	for _, t := range existing {
		if t.Name != ReaderTokenName {
			continue
		}
		if cur, ok := newestPerDevice[t.DeviceID]; !ok || t.CreatedAt.After(cur.CreatedAt) {
			newestPerDevice[t.DeviceID] = t
		}
	}

	// The device id is a label in the op log, not a credential, so even
	// an expired or revoked predecessor is the right thing to inherit:
	// it is the same browser, and the reading history says so.
	deviceID := ""
	var newest time.Time
	for _, t := range newestPerDevice {
		if deviceID == "" || t.CreatedAt.After(newest) {
			deviceID, newest = t.DeviceID, t.CreatedAt
		}
	}

	expiresAt := now.Add(ReaderTokenTTL)
	secret, minted, err := s.mintToken(ctx, userID, ReaderTokenName, readerScopes, &expiresAt, deviceID)
	if err != nil {
		return "", store.Token{}, err
	}

	// Reaping after the mint, not before, is what keeps the count at one
	// for a person reading in one browser: the token just issued is now
	// the newest for this device and carries the id, so the dead ones it
	// replaces have nothing left to hold. Another browser's newest is
	// still spared, dead or not — that is its op-log identity.
	for _, t := range existing {
		if t.Name != ReaderTokenName {
			continue
		}
		if t.DeviceID != minted.DeviceID && t.ID == newestPerDevice[t.DeviceID].ID {
			continue
		}
		if t.RevokedAt != nil || (t.ExpiresAt != nil && now.After(*t.ExpiresAt)) {
			_ = s.St.DeleteToken(ctx, userID, t.ID)
		}
	}
	return secret, minted, nil
}

// RevokeReaderTokens ends browser reading for a user. Signing out has to
// take the reader's credential with it, or a token minted from a session
// would quietly outlive the session that authorised it.
func (s *Service) RevokeReaderTokens(ctx context.Context, userID string) error {
	toks, err := s.St.ListTokens(ctx, userID)
	if err != nil {
		return fmt.Errorf("list tokens: %w", err)
	}
	for _, t := range toks {
		if t.Name != ReaderTokenName || t.RevokedAt != nil {
			continue
		}
		if err := s.St.RevokeToken(ctx, userID, t.ID); err != nil {
			return fmt.Errorf("revoke reader token: %w", err)
		}
	}
	return nil
}

// mintToken is the shared body. An empty deviceID means mint a new one.
func (s *Service) mintToken(ctx context.Context, userID, name string, requested store.ScopeSet, expiresAt *time.Time, deviceID string) (string, store.Token, error) {
	scopes, err := store.NormalizeScopes(requested)
	if err != nil {
		return "", store.Token{}, err
	}
	secret, err := NewSecret()
	if err != nil {
		return "", store.Token{}, err
	}
	id, err := NewSecret()
	if err != nil {
		return "", store.Token{}, err
	}
	if deviceID == "" {
		fresh, err := NewSecret()
		if err != nil {
			return "", store.Token{}, err
		}
		deviceID = fresh[:16] // device ids are short
	}
	tok := store.Token{
		ID:        id,
		UserID:    userID,
		DeviceID:  deviceID,
		Name:      name,
		Scopes:    scopes,
		SHA256:    HashSecret(secret),
		CreatedAt: s.Now(),
		ExpiresAt: expiresAt,
	}
	if err := s.St.CreateToken(ctx, tok); err != nil {
		return "", store.Token{}, err
	}
	return secret, tok, nil
}

// CheckScopeGrant applies the privilege-escalation rule shared by the
// token API and web UI. It is the early check that produces a good
// error message; the rule itself is enforced by the store, inside the
// transaction that writes the token, where it cannot race a demotion.
// The admin CLI intentionally bypasses this pre-check — and is caught
// by the store anyway.
func (s *Service) CheckScopeGrant(ctx context.Context, userID string, scopes store.ScopeSet) error {
	normalized, err := store.NormalizeScopes(scopes)
	if err != nil {
		return err
	}
	if !normalized.Contains(store.ScopeAdmin) {
		return nil
	}
	isAdmin, err := s.IsAdmin(ctx, userID)
	if err != nil {
		return fmt.Errorf("check admin grant: %w", err)
	}
	if !isAdmin {
		return ErrAdminGrantRequiresAdmin
	}
	return nil
}

// IsAdmin reports whether the account is an enabled administrator.
// This is the single definition of "is an admin" — the API's admin
// scope gate and the web UI's admin pages both go through it.
//
// It reads `users.is_admin` (ADR-0013): the role belongs to the
// account, not to a credential, so granting it hands nobody a bearer
// secret, revoking it cannot maim a multi-scope token, and the
// last-admin guard can be a condition inside the transaction that
// writes rather than a scan that races. A disabled account is never an
// admin, whatever its flag says. The role is moved with
// `store.SetUserAdmin`, or out of band with `liseur-sync admin
// grant-admin`.
func (s *Service) IsAdmin(ctx context.Context, userID string) (bool, error) {
	u, err := s.St.UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return u.IsAdmin && u.Enabled(), nil
}

// AuthenticateToken validates a bearer secret and returns its token.
// Revoked and expired tokens are rejected; last_used is touched.
//
// An admin-scoped token is additionally checked against its owner's
// account. Demotion revokes those tokens in the same transaction that
// clears the role, so this should never fire — it is the second fence,
// for a token minted through some future path that forgets, and it
// costs one indexed row read on the only tokens that carry authority
// over the whole instance.
func (s *Service) AuthenticateToken(ctx context.Context, secret string) (store.Token, error) {
	t, err := s.tokenByHashGlobal(ctx, HashSecret(secret))
	if err != nil {
		return t, errors.New("invalid token")
	}
	if t.RevokedAt != nil || (t.ExpiresAt != nil && s.Now().After(*t.ExpiresAt)) {
		return t, errors.New("token revoked or expired")
	}
	if t.Scopes.Contains(store.ScopeAdmin) {
		isAdmin, err := s.IsAdmin(ctx, t.UserID)
		if err != nil {
			return t, err
		}
		if !isAdmin {
			return t, errors.New("admin scope on a non-admin account")
		}
	}
	_ = s.St.TouchToken(ctx, t.UserID, t.ID, s.Now())
	return t, nil
}

// tokenByHashGlobal is a small interface workaround: the store
// interface exposes TokenByHash(userID, hash); the bearer path needs a
// global lookup, which both backends implement. Falling back keeps the
// interface honest if a third backend forgets the global method.
type globalTokenLookup interface {
	TokenByHashGlobal(ctx context.Context, sha256 string) (store.Token, error)
}

func (s *Service) tokenByHashGlobal(ctx context.Context, hash string) (store.Token, error) {
	if g, ok := s.St.(globalTokenLookup); ok {
		return g.TokenByHashGlobal(ctx, hash)
	}
	return store.Token{}, errors.New("store does not support global token lookup")
}

// dummyHash is a valid argon2id encoding used to equalize timing when
// the username does not exist. Password "dummy".
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$M2DdlP2yhB+CZCm2lp3DKbT8NYDMv0hWQRdnJP0bLcU"

// CheckDummyPassword burns the same work a real password check would,
// so an unknown username and a wrong password take comparable time.
// Argon2id at 64 MiB is slow enough that skipping it is a plainly
// measurable signal, which turns any login form into a user
// enumeration oracle. Every path that verifies a password must call
// this when the user is not found.
func CheckDummyPassword(password string) {
	_, _ = CheckPassword(password, dummyHash)
}
