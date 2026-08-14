package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The account operations, shared by the CLI and the web panel
// (ADR-0013). Calling the same store method from two places is not
// enough to keep them honest: the rules that matter — how short a
// password may be, what a name may contain, what a reset does to the
// account's other credentials — are decisions, and a decision made
// twice is a decision that eventually differs. So they live here once,
// as plain functions over a store.Store, and both surfaces are argument
// parsing in front of them.

// MinPasswordLength is the whole password policy. Length is the only
// rule that survives contact with a password manager; complexity
// classes mostly produce Passw0rd!.
const MinPasswordLength = 8

// MaxUserNameLength bounds a name so that it fits a table cell and a
// log line.
const MaxUserNameLength = 64

// Errors the panel and the CLI both render.
var (
	ErrPasswordTooShort = fmt.Errorf(
		"password must be at least %d characters", MinPasswordLength)
	ErrPasswordMismatch = errors.New("passwords do not match")
	ErrNameEmpty        = errors.New("a user name is required")
	ErrNameTooLong      = fmt.Errorf(
		"a user name may be at most %d characters", MaxUserNameLength)
	ErrNameInvalid = errors.New(
		"a user name may contain letters, digits, and - . _ @ only")
	ErrNameTaken = errors.New("that user name is taken")
)

// ValidateUserName is the one definition of an acceptable name. It is
// deliberately narrow: a name reaches a kosync client, a log line, an
// OPDS feed and a filesystem-adjacent path, and every character that
// needs escaping somewhere is a bug waiting for the one place that
// forgot.
func ValidateUserName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return ErrNameEmpty
	case len(name) > MaxUserNameLength:
		return ErrNameTooLong
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if strings.ContainsRune("-._@", r) {
			continue
		}
		return ErrNameInvalid
	}
	return nil
}

// ValidatePassword applies the length rule and, when repeat is given,
// checks the confirmation. Callers with a single field pass "" twice.
func ValidatePassword(pw, repeat string) error {
	if repeat != "" && pw != repeat {
		return ErrPasswordMismatch
	}
	if len(pw) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	return nil
}

// CreateUser makes an account. Both surfaces validate through here, so
// a name the CLI accepts is one the panel accepts.
func CreateUser(ctx context.Context, st store.Store, name, password string) (store.User, error) {
	u, err := newAccount(name, password)
	if err != nil {
		return store.User{}, err
	}
	if err := st.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.User{}, ErrNameTaken
		}
		return store.User{}, err
	}
	return u, nil
}

// ErrSetupClosed is returned when first-run setup is attempted on an
// instance that already has accounts.
var ErrSetupClosed = errors.New("this instance already has an account")

// CreateFirstAdmin makes the instance's first account and gives it the
// admin flag, for the web UI's first-run setup. It is the only path
// where an unauthenticated caller creates an administrator, so the
// "is this instance empty?" question is answered by the store, inside
// the transaction that inserts, rather than here.
func CreateFirstAdmin(ctx context.Context, st store.Store, name, password string) (store.User, error) {
	u, err := newAccount(name, password)
	if err != nil {
		return store.User{}, err
	}
	if err := st.CreateFirstAdmin(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.User{}, ErrSetupClosed
		}
		return store.User{}, err
	}
	u.IsAdmin = true
	return u, nil
}

// newAccount validates and builds an account record without writing it.
func newAccount(name, password string) (store.User, error) {
	if err := ValidateUserName(name); err != nil {
		return store.User{}, err
	}
	if err := ValidatePassword(password, ""); err != nil {
		return store.User{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return store.User{}, err
	}
	id, err := auth.NewSecret()
	if err != nil {
		return store.User{}, err
	}
	return store.User{
		ID:              id[:16],
		Name:            name,
		Argon2Hash:      hash,
		Timezone:        "UTC",
		KosyncEnabled:   true,
		KopluginEnabled: true,
		CreatedAt:       time.Now().UTC(),
	}, nil
}

// SetPassword replaces an account's password. keepSessionID spares one
// auth session — the caller's own, when somebody changes their own
// password; "" when an administrator resets somebody else's, because
// then the point is that nothing survives.
//
// API tokens, kosync slots and koplugin capabilities are deliberately
// left alone: they are separate credentials that a leaked password did
// not expose, and quietly revoking a household's e-readers because
// somebody chose a new password is a bad surprise. Where everything
// must go, that is RevokeAllCredentials, asked for explicitly.
func SetPassword(ctx context.Context, st store.Store, userID, password, keepSessionID string) error {
	if err := ValidatePassword(password, ""); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return st.SetUserPassword(ctx, userID, hash, keepSessionID)
}

// RevokeAllCredentials cuts every way into an account: tokens,
// sessions, kosync slots, koplugin capabilities and unredeemed pairing
// codes, in one transaction. The account itself is untouched — its
// owner signs in with their password and enrols devices again.
func RevokeAllCredentials(ctx context.Context, st store.Store, userID string) error {
	return st.RevokeAllUserCredentials(ctx, userID, time.Now().UTC())
}
