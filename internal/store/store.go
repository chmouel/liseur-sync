// Package store defines the storage interface for liseur-sync, with
// SQLite and PostgreSQL backends. All queries are scoped by user_id;
// no caller constructs cross-user queries.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Common sentinel errors.
var (
	ErrNotFound   = errors.New("store: not found")
	ErrConflict   = errors.New("store: conflict") // uniqueness or state conflict
	ErrIDMismatch = errors.New("store: idempotent id reused with different payload")
)

// TokenPurgeGrace is how long expired or revoked tokens remain listed
// (for the UI) before Housekeep deletes them.
const TokenPurgeGrace = 30 * 24 * time.Hour

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

// WorkResolution is the atomic result of resolving ordered identifiers.
// ConflictingWorkIDs is non-empty when the identifiers name multiple works;
// in that case no work-graph mutation is committed.
type WorkResolution struct {
	WorkID             string
	Confidence         string // "high" | "low"
	Created            bool
	ConflictingWorkIDs []string
}

// DecideWorkResolution applies the protocol decision to identifiers already
// ordered strongest-first. matches is keyed by "kind:value".
func DecideWorkResolution(ids []Identifier, matches map[string]string, confirmed bool) WorkResolution {
	var result WorkResolution
	seen := make(map[string]bool)
	firstKind := ""
	for _, id := range ids {
		workID, ok := matches[id.Kind+":"+id.Value]
		if !ok {
			continue
		}
		if firstKind == "" {
			firstKind = id.Kind
			result.WorkID = workID
		}
		if !seen[workID] {
			seen[workID] = true
			result.ConflictingWorkIDs = append(result.ConflictingWorkIDs, workID)
		}
	}
	if len(result.ConflictingWorkIDs) > 1 {
		result.WorkID = ""
		return result
	}
	result.ConflictingWorkIDs = nil
	result.Confidence = "high"
	if firstKind == "ta" && !confirmed {
		result.Confidence = "low"
	}
	return result
}

// Op is one position operation in the append-only per-user log.
type Op struct {
	UserID      string
	Seq         int64
	OpID        string // client-generated deterministic opaque id
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

// SessionFingerprint identifies the immutable client payload. It is
// retained after rollup so old inferred/native sessions remain
// idempotent without keeping their full rows.
func SessionFingerprint(s Session) string {
	edition, alias, source := "", "", ""
	if s.EditionSHA != nil {
		edition = *s.EditionSHA
	}
	if s.OriginAlias != nil {
		alias = *s.OriginAlias
	}
	if s.SourceKey != nil {
		source = *s.SourceKey
	}
	// Inferred-session provenance may be backfilled after deployment.
	// It does not change the immutable reading fact.
	if s.Origin == OriginInferred {
		alias = ""
		source = ""
	}
	raw := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%.17g\x00%.17g\x00%d\x00%s\x00%s\x00%s",
		s.WorkID, edition, s.DeviceID, s.StartedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano),
		s.EndedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano), s.SessionID, s.StartProg, s.EndProg,
		s.IdleMs, s.Origin, alias, source)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// SessionRollup is a per-(work, tz-local day) aggregate of sessions
// older than the retention window. Raw session rows past the horizon
// are reduced to these daily totals; days are fixed in the user's
// timezone at rollup time and are not re-bucketed if the user later
// changes timezone.
type SessionRollup struct {
	UserID        string
	WorkID        string
	Day           string // YYYY-MM-DD
	ActiveSeconds float64
	Pages         float64
	ProgDelta     float64
	SessionCount  int64
}

// OpResult is the per-item outcome of a batch push.
type OpResult struct {
	OpID   string
	Status string // "applied" | "duplicate" | "conflict" | "invalid"
	Seq    int64  // when applied or duplicate
	Reason string // when conflict or invalid
}

// InferredSessionGroup is one closed kosync op group and the session
// derived from that exact snapshot.
type InferredSessionGroup struct {
	Session Session
	Ops     []Op
}

// InferenceOpFingerprint captures the fields used to group and
// materialize one position op.
func InferenceOpFingerprint(o Op) string {
	edition, alias := "", ""
	if o.EditionSHA != nil {
		edition = *o.EditionSHA
	}
	if o.OriginAlias != nil {
		alias = *o.OriginAlias
	}
	raw := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s\x00%.17g\x00%s\x00%s\x00%s",
		o.Seq, o.OpID, o.WorkID, edition, o.DeviceID, o.Progression, o.Origin, alias,
		o.ReceivedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano))
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ValidInferredSessionGroup verifies that the session was derived from
// the exact ordered kosync snapshot supplied with it.
func ValidInferredSessionGroup(group InferredSessionGroup) bool {
	if len(group.Ops) == 0 || group.Session.SessionID == "" ||
		group.Session.Origin != OriginInferred {
		return false
	}
	first, last := group.Ops[0], group.Ops[len(group.Ops)-1]
	for i, op := range group.Ops {
		if op.Origin != OriginKosync || op.WorkID != last.WorkID ||
			op.DeviceID != last.DeviceID ||
			!equalOptionalString(op.EditionSHA, last.EditionSHA) ||
			!equalOptionalString(op.OriginAlias, last.OriginAlias) ||
			(i > 0 && op.ReceivedAt.Before(group.Ops[i-1].ReceivedAt)) {
			return false
		}
	}
	ses := group.Session
	return ses.WorkID == last.WorkID && ses.DeviceID == last.DeviceID &&
		ses.StartedAt.Equal(first.ReceivedAt) && ses.EndedAt.Equal(last.ReceivedAt) &&
		ses.StartProg == first.Progression && ses.EndProg == last.Progression &&
		equalOptionalString(ses.EditionSHA, last.EditionSHA) &&
		equalOptionalString(ses.OriginAlias, last.OriginAlias)
}

func equalOptionalString(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
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
	// ResolveWork atomically resolves identifiers ordered strongest-first,
	// creates the proposed work when none match, and promotes missing aliases
	// only for a high-confidence result.
	ResolveWork(ctx context.Context, userID string, proposed Work, editions []Edition, ids []Identifier, confirmed bool) (WorkResolution, error)
	// ResolveAliases reports the current alias graph without mutation.
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
	UpdateUserPassword(ctx context.Context, userID, argon2Hash string) error

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
	PendingInferenceOps(ctx context.Context, userID string) ([]Op, error)
	HeadsFor(ctx context.Context, userID string) (Heads, error)
	CompactionHorizon(ctx context.Context, userID string) (int64, error)
	Compact(ctx context.Context, userID string, olderThan time.Time) (newHorizon int64, err error)

	// Sessions.
	AppendSessions(ctx context.Context, userID string, ss []Session) error
	AppendInferredSession(ctx context.Context, userID string, group InferredSessionGroup) error
	SessionsInRange(ctx context.Context, userID string, from, to time.Time) ([]Session, error)
	SessionsForWork(ctx context.Context, userID, workID string, limit int) ([]Session, error)
	CurrentSessionsForWork(ctx context.Context, userID, workID string, limit int) ([]Session, error)
	WorkIDsWithInsights(ctx context.Context, userID string) ([]string, error)
	EditionBySHA(ctx context.Context, userID, sha256 string) (Edition, error)

	// Session rollups (retention). SessionsEndedBefore feeds the rollup
	// job; ApplyRollups additively upserts daily aggregates and deletes
	// the rolled-up raw sessions in one transaction (supersession rows
	// cascade). Day bounds are inclusive YYYY-MM-DD strings.
	SessionsEndedBefore(ctx context.Context, userID string, before time.Time) ([]Session, error)
	ApplyRollups(ctx context.Context, userID string, rollups []SessionRollup, deleteSessions []Session) error
	RollupsInRange(ctx context.Context, userID, fromDay, toDay string) ([]SessionRollup, error)
	RollupsForWork(ctx context.Context, userID, workID string) ([]SessionRollup, error)

	// Housekeep deletes expired auth debris: expired pairing codes,
	// expired or revoked auth sessions, and tokens expired/revoked more
	// than TokenPurgeGrace ago. Global (all users) by design, like
	// UserIDs: it runs from the background maintenance loop.
	Housekeep(ctx context.Context, now time.Time) error

	// Auth sessions (login credentials and web sessions).
	CreateAuthSession(ctx context.Context, a AuthSession) error
	AuthSessionByHash(ctx context.Context, sha256 string) (AuthSession, error)
	RevokeAuthSession(ctx context.Context, userID, id string) error

	// kosync pairing codes and device credentials.
	CreatePairingCode(ctx context.Context, p PairingCode) error
	RedeemPairingCode(ctx context.Context, codeSHA256 string, at time.Time) (PairingCode, error)
	CreateKosyncDevice(ctx context.Context, d KosyncDevice) error
	KosyncDeviceByKey(ctx context.Context, keySHA256 string) (KosyncDevice, error)
	AppendKosyncOp(ctx context.Context, userID, partialMD5, deviceID string, op Op) (OpResult, error)
	CreateKopluginDevice(ctx context.Context, d KopluginDevice) error
	KopluginDeviceByToken(ctx context.Context, tokenSHA256 string) (KopluginDevice, error)
	UpsertKopluginSession(ctx context.Context, userID string, ses Session) (status string, err error)
	UpsertKopluginSessionByAlias(ctx context.Context, userID, partialMD5 string, ses Session) (status string, err error)
	CreatePendingWork(ctx context.Context, userID string, partialMD5 string) (workID string, created bool, err error)
	WorkIDByAlias(ctx context.Context, userID, kind, value string) (string, error)
}

// KopluginDevice is a capability-URL credential for the KOReader
// statistics plugin adapter. Upload-only; the request's device_id is
// derived from this row.
type KopluginDevice struct {
	ID          string
	UserID      string
	TokenSHA256 string
	Label       string
	DeviceID    string
	CreatedAt   time.Time
	RevokedAt   *time.Time
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
	Work        Work
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
