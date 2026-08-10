// Package store defines the storage interface for liseur-sync, with
// SQLite and PostgreSQL backends. All queries are scoped by user_id;
// no caller constructs cross-user queries.
package store

import (
	"context"
	"errors"
	"time"
)

// Common sentinel errors.
var (
	ErrNotFound   = errors.New("store: not found")
	ErrConflict   = errors.New("store: conflict")             // uniqueness or state conflict
	ErrIDMismatch = errors.New("store: idempotent id reused with different payload")
)

// Scope is a token scope.
type Scope string

const (
	ScopeSync         Scope = "sync"
	ScopeReadInsights Scope = "read-insights"
	ScopeAdmin        Scope = "admin"
)

// Origin of a record: which protocol it arrived on.
type Origin string

const (
	OriginNative   Origin = "native"
	OriginKosync   Origin = "kosync"
	OriginKoplugin Origin = "koplugin"
	OriginInferred Origin = "inferred"
)

// User is an account.
type User struct {
	ID              string
	Name            string
	Argon2Hash      string
	Timezone        string // IANA
	KosyncEnabled   bool
	KopluginEnabled bool
	CreatedAt       time.Time
}

// Token is a per-device API token. Hash is SHA-256 of the secret.
type Token struct {
	ID        string
	UserID    string
	DeviceID  string
	Name      string
	Scope     Scope
	SHA256    string
	CreatedAt time.Time
	ExpiresAt *time.Time
	LastUsed  *time.Time
	RevokedAt *time.Time
}

// Work is the abstract book positions and statistics attach to.
type Work struct {
	ID        string
	UserID    string
	Title     string
	Author    string
	Pending   bool
	CreatedAt time.Time
}

// Edition is one concrete file of a work.
type Edition struct {
	UserID    string
	SHA256    string // hex, lowercase
	WorkID    string
	PageCount *int64
	CharCount *int64
	MetaJSON  []byte
}

// Alias maps an observed identifier to a work.
type Alias struct {
	UserID string
	Kind   string // sha256 | partial-md5 | dc | ta
	Value  string
	WorkID string
}

// Op is one position operation in the append-only per-user log.
type Op struct {
	UserID      string
	Seq         int64
	OpID        string // client-generated UUIDv7
	WorkID      string
	EditionSHA  *string
	DeviceID    string // server-side, from the token
	ClientTS    time.Time
	Progression float64
	LocatorJSON []byte
	ForeignPos  *string
	Origin      Origin
	OriginAlias *string
	ReceivedAt  time.Time
}

// Session is one reading session fact.
type Session struct {
	UserID      string
	SessionID   string
	WorkID      string
	EditionSHA  *string
	DeviceID    string
	StartedAt   time.Time
	EndedAt     time.Time
	StartProg   float64
	EndProg     float64
	IdleMs      int64
	Origin      Origin
	OriginAlias *string
	SourceKey   *string // koplugin legacy upsert key
	ReceivedAt  time.Time
}

// OpResult is the per-item outcome of a batch push.
type OpResult struct {
	OpID   string
	Status string // "applied" | "duplicate" | "conflict" | "invalid"
	Seq    int64  // when applied or duplicate
	Reason string // when conflict or invalid
}

// ChangesPage is one page of the delta-sync stream.
type ChangesPage struct {
	Ops          []Op
	HighWater    int64 // current max seq for the user
	HasMore      bool
	ResyncNeeded bool // since < compaction horizon
}

// Heads is the recovery snapshot: newest op per (work, device).
type Heads struct {
	Ops         []Op // newest op per (work_id, device_id)
	SnapshotSeq int64
}

// Store is the storage contract. Implementations: SQLite and Postgres.
// All methods take a userID; cross-user access is impossible by
// construction.
type Store interface {
	// Lifecycle.
	Migrate(ctx context.Context) error
	Close() error

	// Users.
	CreateUser(ctx context.Context, u User) error
	UserByName(ctx context.Context, name string) (User, error)
	UserByID(ctx context.Context, userID string) (User, error)
	UserIDs(ctx context.Context) ([]string, error)
	ListUsers(ctx context.Context) ([]User, error)

	// Tokens.
	CreateToken(ctx context.Context, t Token) error
	TokenByHash(ctx context.Context, userID, sha256 string) (Token, error)
	ListTokens(ctx context.Context, userID string) ([]Token, error)
	RevokeToken(ctx context.Context, userID, tokenID string) error
	TouchToken(ctx context.Context, userID, tokenID string, at time.Time) error

	// Works / editions / aliases.
	// ResolveAliases returns the distinct work IDs that any of the given
	// aliases currently map to, in alias-priority order per identifier.
	ResolveAliases(ctx context.Context, userID string, ids []Identifier) (map[string]string, error)
	CreateWork(ctx context.Context, w Work, e *Edition, ids []Identifier) error
	AddAliases(ctx context.Context, userID, workID string, ids []Identifier) error
	ClearPending(ctx context.Context, userID, workID string) error
	WorkByID(ctx context.Context, userID, workID string) (Work, error)
	ListWorks(ctx context.Context, userID string) ([]WorkSummary, error)
	SplitWork(ctx context.Context, userID, workID, editionSHA string, aliasValues []Identifier, newWork Work) error
	MergeWorks(ctx context.Context, userID, fromWorkID, intoWorkID string) error

	// User settings.
	UpdateUserSettings(ctx context.Context, userID, timezone string, kosyncEnabled, kopluginEnabled bool) error

	// Invites (admin).
	CreateInvite(ctx context.Context, inv Invite) error
	ListInvites(ctx context.Context, userID string) ([]Invite, error)
	RevokeInvite(ctx context.Context, userID, inviteID string) error
	RedeemInvite(ctx context.Context, codeSHA256 string, at time.Time) (Invite, error)

	// Device credentials (list/revoke for the UI).
	ListKosyncDevices(ctx context.Context, userID string) ([]KosyncDevice, error)
	RevokeKosyncDevice(ctx context.Context, userID, slot string) error
	ListKopluginDevices(ctx context.Context, userID string) ([]KopluginDevice, error)
	RevokeKopluginDevice(ctx context.Context, userID, id string) error

	// Ops.
	AppendOps(ctx context.Context, userID, deviceID string, ops []Op) ([]OpResult, error)
	Changes(ctx context.Context, userID string, since int64, limit int) (ChangesPage, error)
	Positions(ctx context.Context, userID, workID string, limit int) ([]Op, error)
	OpsBefore(ctx context.Context, userID string, before time.Time) ([]Op, error)
	HeadsFor(ctx context.Context, userID string) (Heads, error)
	CompactionHorizon(ctx context.Context, userID string) (int64, error)
	Compact(ctx context.Context, userID string, olderThan time.Time) (newHorizon int64, err error)

	// Sessions.
	AppendSessions(ctx context.Context, userID string, ss []Session) error
	SessionsInRange(ctx context.Context, userID string, from, to time.Time) ([]Session, error)
	SessionsForWork(ctx context.Context, userID, workID string, limit int) ([]Session, error)
	EditionBySHA(ctx context.Context, userID, sha256 string) (Edition, error)

	// Auth sessions (login credentials and web sessions).
	CreateAuthSession(ctx context.Context, a AuthSession) error
	AuthSessionByHash(ctx context.Context, sha256 string) (AuthSession, error)
	RevokeAuthSession(ctx context.Context, userID, id string) error

	// kosync pairing codes and device credentials.
	CreatePairingCode(ctx context.Context, p PairingCode) error
	RedeemPairingCode(ctx context.Context, codeSHA256 string, at time.Time) (PairingCode, error)
	CreateKosyncDevice(ctx context.Context, d KosyncDevice) error
	KosyncDeviceByKey(ctx context.Context, keySHA256 string) (KosyncDevice, error)
	CreateKopluginDevice(ctx context.Context, d KopluginDevice) error
	KopluginDeviceByToken(ctx context.Context, tokenSHA256 string) (KopluginDevice, error)
	UpsertKopluginSession(ctx context.Context, userID string, ses Session) (status string, err error)
	CreatePendingWork(ctx context.Context, userID string, partialMD5 string) (workID string, created bool, err error)
	WorkIDByAlias(ctx context.Context, userID, kind, value string) (string, error)
}

// KopluginDevice is a capability-URL credential for the KOReader
// statistics plugin adapter. Upload-only; the request's device_id is
// derived from this row.
type KopluginDevice struct {
	ID           string
	UserID       string
	TokenSHA256  string
	Label        string
	DeviceID     string
	CreatedAt    time.Time
	RevokedAt    *time.Time
}

// PairingCode is a one-time code used to bind a kosync device slot.
// Stored hashed; expires; redeemed atomically single-use.
type PairingCode struct {
	ID         string
	UserID     string
	CodeSHA256 string
	ExpiresAt  time.Time
	UsedAt     *time.Time
}

// KosyncDevice is a kosync device credential slot.
type KosyncDevice struct {
	UserID     string
	DeviceSlot string
	KeySHA256  string
	Label      string
	RevokedAt  *time.Time
}

// AuthSession is a short-lived login credential (kind=login) or a web
// UI session (kind=web). Hashed at rest; revocable.
type AuthSession struct {
	ID        string
	UserID    string
	SHA256    string
	Kind      string // "login" | "web"
	CSRFHash  string // sha256 of per-session CSRF token (web only)
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// Identifier is a (kind, value) alias observed in the wild.
type Identifier struct {
	Kind  string
	Value string
}

// WorkSummary is a work plus its current head position for list views.
type WorkSummary struct {
	Work       Work
	Progression *float64 // newest op's progression, nil if none
	LastActive  *time.Time
	Pending     bool
}

// Invite is an admin-generated registration code (stored hashed).
type Invite struct {
	ID         string
	CodeSHA256 string
	CreatedBy  string
	ExpiresAt  time.Time
	UsedBy     *string
	UsedAt     *time.Time
}
