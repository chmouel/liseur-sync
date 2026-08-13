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
// Previous reader tokens are left alone unless they have already
// expired. Revoking them here would let two open tabs invalidate each
// other's credential in a loop, each re-minting in response to the
// other's failure.
func (s *Service) MintReaderToken(ctx context.Context, userID string) (string, store.Token, error) {
	existing, err := s.St.ListTokens(ctx, userID)
	if err != nil {
		return "", store.Token{}, fmt.Errorf("list tokens: %w", err)
	}
	now := s.Now()
	deviceID := ""
	var newest time.Time
	for _, t := range existing {
		if t.Name != ReaderTokenName {
			continue
		}
		// The device id is a label in the op log, not a credential, so
		// even a revoked predecessor is the right thing to inherit: it
		// is the same browser, and the reading history says so.
		if deviceID == "" || t.CreatedAt.After(newest) {
			deviceID, newest = t.DeviceID, t.CreatedAt
		}
		if t.RevokedAt == nil && t.ExpiresAt != nil && now.After(*t.ExpiresAt) {
			_ = s.St.RevokeToken(ctx, userID, t.ID)
		}
	}
	expiresAt := now.Add(ReaderTokenTTL)
	return s.mintToken(ctx, userID, ReaderTokenName, readerScopes, &expiresAt, deviceID)
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
// token API and web UI. The admin CLI intentionally bypasses it.
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

// IsAdmin reports whether the user holds an active admin-scope token.
// This is the single definition of "is an admin" — the API's admin
// scope gate and the web UI's admin pages both go through it, so a
// user can never bootstrap admin rights for themselves. Admin tokens
// are minted out of band by `liseur-sync admin mint-token -scope
// admin`.
func (s *Service) IsAdmin(ctx context.Context, userID string) (bool, error) {
	toks, err := s.St.ListTokens(ctx, userID)
	if err != nil {
		return false, err
	}
	now := s.Now()
	for _, t := range toks {
		if !t.Scopes.Contains(store.ScopeAdmin) || t.RevokedAt != nil {
			continue
		}
		if t.ExpiresAt != nil && now.After(*t.ExpiresAt) {
			continue
		}
		return true, nil
	}
	return false, nil
}

// AuthenticateToken validates a bearer secret and returns its token.
// Revoked and expired tokens are rejected; last_used is touched.
func (s *Service) AuthenticateToken(ctx context.Context, secret string) (store.Token, error) {
	t, err := s.tokenByHashGlobal(ctx, HashSecret(secret))
	if err != nil {
		return t, errors.New("invalid token")
	}
	if t.RevokedAt != nil || (t.ExpiresAt != nil && s.Now().After(*t.ExpiresAt)) {
		return t, errors.New("token revoked or expired")
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
