package auth

import (
	"context"
	"errors"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// LoginTTL is the lifetime of the short-lived auth credential returned
// by login (usable only for token management).
const LoginTTL = time.Hour

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
		// Constant-work defense: check against a dummy hash so unknown
		// users and wrong passwords take the same path.
		_, _ = CheckPassword(password, dummyHash)
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
func (s *Service) CreateToken(ctx context.Context, loginSecret, name string, scope store.Scope, expiresAt *time.Time) (plaintext string, tok store.Token, err error) {
	userID, err := s.AuthenticateLogin(ctx, loginSecret)
	if err != nil {
		return "", tok, err
	}
	return s.MintToken(ctx, userID, name, scope, expiresAt)
}

// MintToken creates a token for a known user (admin CLI path).
func (s *Service) MintToken(ctx context.Context, userID, name string, scope store.Scope, expiresAt *time.Time) (string, store.Token, error) {
	secret, err := NewSecret()
	if err != nil {
		return "", store.Token{}, err
	}
	id, err := NewSecret()
	if err != nil {
		return "", store.Token{}, err
	}
	deviceID, err := NewSecret()
	if err != nil {
		return "", store.Token{}, err
	}
	tok := store.Token{
		ID:        id,
		UserID:    userID,
		DeviceID:  deviceID[:16], // device ids are short
		Name:      name,
		Scope:     scope,
		SHA256:    HashSecret(secret),
		CreatedAt: s.Now(),
		ExpiresAt: expiresAt,
	}
	if err := s.St.CreateToken(ctx, tok); err != nil {
		return "", store.Token{}, err
	}
	return secret, tok, nil
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
