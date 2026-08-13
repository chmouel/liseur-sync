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
	"sort"
	"strings"
	"time"
)

// Common sentinel errors.
var (
	ErrNotFound            = errors.New("store: not found")
	ErrConflict            = errors.New("store: conflict") // uniqueness or state conflict
	ErrIDMismatch          = errors.New("store: idempotent id reused with different payload")
	ErrIdempotencyConflict = errors.New("store: idempotency key reused with different request")
	ErrInvalidTransition   = errors.New("store: invalid state transition")
	ErrStaleRevision       = errors.New("store: stale revision")
)

// TokenPurgeGrace is how long expired or revoked tokens remain listed
// (for the UI) before Housekeep deletes them.
const TokenPurgeGrace = 30 * 24 * time.Hour

// Scope is a token scope.
type Scope string

const (
	ScopeSync          Scope = "sync"
	ScopeReadInsights  Scope = "read-insights"
	ScopeLibraryRead   Scope = "library-read"
	ScopeLibraryManage Scope = "library-manage"
	ScopeAdmin         Scope = "admin"
)

var scopeOrder = map[Scope]int{
	ScopeSync:          0,
	ScopeReadInsights:  1,
	ScopeLibraryRead:   2,
	ScopeLibraryManage: 3,
	ScopeAdmin:         4,
}

// ScopeSet is the canonical, duplicate-free set of capabilities on a token.
type ScopeSet []Scope

// NormalizeScopes validates, deduplicates, and orders a scope set.
func NormalizeScopes(scopes []Scope) (ScopeSet, error) {
	if len(scopes) == 0 {
		return nil, errors.New("at least one scope is required")
	}
	seen := make(map[Scope]bool, len(scopes))
	out := make(ScopeSet, 0, len(scopes))
	for _, scope := range scopes {
		if _, ok := scopeOrder[scope]; !ok {
			return nil, fmt.Errorf("invalid scope %q", scope)
		}
		if !seen[scope] {
			seen[scope] = true
			out = append(out, scope)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return scopeOrder[out[i]] < scopeOrder[out[j]]
	})
	return out, nil
}

// Contains reports whether a scope is explicitly present.
func (s ScopeSet) Contains(scope Scope) bool {
	for _, candidate := range s {
		if candidate == scope {
			return true
		}
	}
	return false
}

// Allows reports whether the set grants a requested capability.
func (s ScopeSet) Allows(scope Scope) bool {
	if s.Contains(ScopeAdmin) || s.Contains(scope) {
		return true
	}
	return scope == ScopeLibraryRead && s.Contains(ScopeLibraryManage)
}

// Legacy returns the deprecated scalar representation for singleton sets.
func (s ScopeSet) Legacy() (Scope, bool) {
	if len(s) != 1 {
		return "", false
	}
	return s[0], true
}

func (s ScopeSet) String() string {
	values := make([]string, len(s))
	for i, scope := range s {
		values[i] = string(scope)
	}
	return strings.Join(values, ",")
}

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
	Scopes    ScopeSet
	SHA256    string
	CreatedAt time.Time
	ExpiresAt *time.Time
	LastUsed  *time.Time
	RevokedAt *time.Time
}

// LibraryKind controls whether content is server-managed or discovered from
// an administrator-configured read-only root.
type LibraryKind string

const (
	LibraryManaged LibraryKind = "managed"
	LibraryWatched LibraryKind = "watched"
)

// LibraryRole is an ACL capability. Manage implies read.
type LibraryRole string

const (
	LibraryRoleRead   LibraryRole = "read"
	LibraryRoleManage LibraryRole = "manage"
)

func (r LibraryRole) Valid() bool {
	return r == LibraryRoleRead || r == LibraryRoleManage
}

func (r LibraryRole) Allows(required LibraryRole) bool {
	return r == LibraryRoleManage || r == required
}

// Library is one shared catalog namespace.
type Library struct {
	ID          string
	OwnerUserID string
	QuotaUserID string
	Kind        LibraryKind
	Name        string
	RootPath    *string
	ConfigJSON  []byte
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AccessibleLibrary includes the caller's effective ACL role.
type AccessibleLibrary struct {
	Library Library
	Role    LibraryRole
}

// BookStatus is the catalog lifecycle state.
type BookStatus string

const (
	BookActive  BookStatus = "active"
	BookMissing BookStatus = "missing"
	BookTrashed BookStatus = "trashed"
	BookReview  BookStatus = "review"
)

// MetadataSource records which precedence stage supplied a field.
type MetadataSource string

const (
	MetadataEmbedded MetadataSource = "embedded"
	MetadataFilename MetadataSource = "filename"
	MetadataExternal MetadataSource = "external"
	MetadataManual   MetadataSource = "manual"
)

// CatalogBook is shared through a library ACL. Its sync Work remains
// user-scoped and is joined through UserBookWork.
type CatalogBook struct {
	ID                  string
	LibraryID           string
	Status              BookStatus
	Title               string
	TitleSource         MetadataSource
	TitleLocked         bool
	Subtitle            string
	SubtitleSource      MetadataSource
	SubtitleLocked      bool
	Description         string
	DescriptionSource   MetadataSource
	DescriptionLocked   bool
	Publisher           string
	PublisherSource     MetadataSource
	PublisherLocked     bool
	PublishedDate       string
	PublishedDateSource MetadataSource
	PublishedDateLocked bool
	RawMetadataJSON     []byte
	CreatedAt           time.Time
	UpdatedAt           time.Time
	TrashedAt           *time.Time
	TrashExpiresAt      *time.Time
}

// UserBookWork is the privacy boundary between a shared catalog book and
// one user's sync graph.
type UserBookWork struct {
	UserID    string
	LibraryID string
	BookID    string
	WorkID    string
	CreatedAt time.Time
}

// IngestSource identifies how content entered the durable ingestion queue.
type IngestSource string

const (
	IngestUpload  IngestSource = "upload"
	IngestWatched IngestSource = "watched"
)

func (s IngestSource) Valid() bool {
	return s == IngestUpload || s == IngestWatched
}

// IngestState is one durable stage of content ingestion.
type IngestState string

const (
	IngestReceived    IngestState = "received"
	IngestStaged      IngestState = "staged"
	IngestValidated   IngestState = "validated"
	IngestExtracted   IngestState = "extracted"
	IngestPromoted    IngestState = "promoted"
	IngestQuarantined IngestState = "quarantined"
	IngestFailed      IngestState = "failed"
)

func (s IngestState) Valid() bool {
	switch s {
	case IngestReceived, IngestStaged, IngestValidated, IngestExtracted,
		IngestPromoted, IngestQuarantined, IngestFailed:
		return true
	default:
		return false
	}
}

// CanTransitionIngest reports whether a generic job transition is legal.
// Promotion is deliberately excluded: it must commit the durable blob,
// catalog reference, quota reservation, and promoted state together.
func CanTransitionIngest(from, to IngestState) bool {
	switch from {
	case IngestReceived:
		return to == IngestStaged || to == IngestFailed
	case IngestStaged:
		return to == IngestValidated || to == IngestQuarantined || to == IngestFailed
	case IngestValidated:
		return to == IngestExtracted || to == IngestQuarantined || to == IngestFailed
	case IngestExtracted:
		return to == IngestQuarantined || to == IngestFailed
	case IngestQuarantined:
		return to == IngestStaged
	case IngestFailed:
		return to == IngestReceived || to == IngestStaged
	default:
		return false
	}
}

// IngestJob is the persisted ingestion state. RequestFingerprint describes
// immutable request metadata, not the uploaded content digest.
type IngestJob struct {
	ID                 string
	UserID             string
	LibraryID          string
	QuotaUserID        string
	Source             IngestSource
	ClientKey          *string
	RequestFingerprint string
	State              IngestState
	BytesReceived      int64
	ContentSHA256      *string
	StagingPath        *string
	SourceRelativePath *string
	BookID             *string
	ErrorCode          *string
	ErrorDetail        *string
	RetryCount         int64
	Revision           int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ExpiresAt          *time.Time
}

// IngestJobRequest contains the immutable fields used to create or replay a
// durable job. User and quota principals are derived by the store.
type IngestJobRequest struct {
	ID                 string
	LibraryID          string
	Source             IngestSource
	ClientKey          *string
	RequestFingerprint string
	SourceRelativePath *string
	CreatedAt          time.Time
}

// IngestJobCursor is the exclusive cursor for stable job pagination.
type IngestJobCursor struct {
	CreatedAt time.Time
	ID        string
}

// IngestJobTransition applies one revision-checked state change. Content
// fields may only be supplied while moving a received job to staged or
// failed. Error fields are required for failed/quarantined targets.
type IngestJobTransition struct {
	ExpectedState    IngestState
	ExpectedRevision int64
	NextState        IngestState
	BytesReceived    *int64
	ContentSHA256    *string
	StagingPath      *string
	ErrorCode        string
	ErrorDetail      string
	ExpiresAt        *time.Time
	IncrementRetry   bool
	UpdatedAt        time.Time
}

// ValidateIngestJobRequest checks invariants shared by every backend.
func ValidateIngestJobRequest(request IngestJobRequest) error {
	if request.ID == "" || request.LibraryID == "" {
		return errors.New("ingest job id and library id are required")
	}
	if !request.Source.Valid() {
		return fmt.Errorf("invalid ingest source %q", request.Source)
	}
	if request.CreatedAt.IsZero() {
		return errors.New("ingest job creation time is required")
	}
	if request.RequestFingerprint == "" || len(request.RequestFingerprint) > 512 {
		return errors.New("invalid ingest request fingerprint")
	}
	if request.ClientKey != nil &&
		(*request.ClientKey == "" || len(*request.ClientKey) > 256) {
		return errors.New("invalid ingest client key")
	}
	switch request.Source {
	case IngestUpload:
		if request.SourceRelativePath != nil {
			return errors.New("upload job cannot have a source path")
		}
	case IngestWatched:
		if request.SourceRelativePath == nil || *request.SourceRelativePath == "" ||
			len(*request.SourceRelativePath) > 4096 {
			return errors.New("watched job requires a source path")
		}
	}
	return nil
}

// ApplyIngestTransition validates and applies a transition without mutating
// immutable job fields.
func ApplyIngestTransition(current IngestJob, change IngestJobTransition) (IngestJob, error) {
	if current.State != change.ExpectedState ||
		current.Revision != change.ExpectedRevision {
		return IngestJob{}, ErrStaleRevision
	}
	if change.ExpectedRevision < 1 || change.UpdatedAt.IsZero() ||
		change.UpdatedAt.Before(current.UpdatedAt) ||
		!change.ExpectedState.Valid() || !change.NextState.Valid() ||
		!CanTransitionIngest(change.ExpectedState, change.NextState) {
		return IngestJob{}, ErrInvalidTransition
	}
	retrying := change.ExpectedState == IngestFailed ||
		change.ExpectedState == IngestQuarantined
	if change.IncrementRetry != retrying {
		return IngestJob{}, ErrInvalidTransition
	}
	if change.BytesReceived != nil && *change.BytesReceived < 0 {
		return IngestJob{}, ErrInvalidTransition
	}
	contentUpdate := change.ContentSHA256 != nil || change.StagingPath != nil ||
		change.BytesReceived != nil
	if contentUpdate && change.ExpectedState != IngestReceived {
		return IngestJob{}, ErrInvalidTransition
	}
	if (change.ContentSHA256 == nil) != (change.StagingPath == nil) {
		return IngestJob{}, ErrInvalidTransition
	}
	if change.ContentSHA256 != nil &&
		(*change.ContentSHA256 == "" || len(*change.ContentSHA256) > 128 ||
			*change.StagingPath == "" || len(*change.StagingPath) > 4096) {
		return IngestJob{}, ErrInvalidTransition
	}
	if change.ExpectedState == IngestReceived &&
		change.NextState == IngestStaged && change.BytesReceived == nil {
		return IngestJob{}, ErrInvalidTransition
	}
	failing := change.NextState == IngestFailed ||
		change.NextState == IngestQuarantined
	if failing {
		if change.ErrorCode == "" || len(change.ErrorCode) > 128 ||
			len(change.ErrorDetail) > 4096 || change.ExpiresAt == nil ||
			!change.ExpiresAt.After(change.UpdatedAt) {
			return IngestJob{}, ErrInvalidTransition
		}
	} else if change.ErrorCode != "" || change.ErrorDetail != "" ||
		change.ExpiresAt != nil {
		return IngestJob{}, ErrInvalidTransition
	}

	next := current
	if change.BytesReceived != nil {
		next.BytesReceived = *change.BytesReceived
	}
	if change.ContentSHA256 != nil {
		next.ContentSHA256 = change.ContentSHA256
		next.StagingPath = change.StagingPath
	}
	if change.NextState == IngestStaged &&
		(next.ContentSHA256 == nil || next.StagingPath == nil) {
		return IngestJob{}, ErrInvalidTransition
	}
	if change.NextState == IngestReceived &&
		(next.ContentSHA256 != nil || next.StagingPath != nil) {
		return IngestJob{}, ErrInvalidTransition
	}
	next.State = change.NextState
	next.UpdatedAt = change.UpdatedAt
	next.Revision++
	if change.IncrementRetry {
		next.RetryCount++
	}
	if failing {
		next.ErrorCode = &change.ErrorCode
		if change.ErrorDetail != "" {
			next.ErrorDetail = &change.ErrorDetail
		} else {
			next.ErrorDetail = nil
		}
		next.ExpiresAt = change.ExpiresAt
	} else {
		next.ErrorCode = nil
		next.ErrorDetail = nil
		next.ExpiresAt = nil
	}
	return next, nil
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
	UpdateTokenScopes(ctx context.Context, userID, tokenID string, scopes ScopeSet) error
	RevokeToken(ctx context.Context, userID, tokenID string) error
	TouchToken(ctx context.Context, userID, tokenID string, at time.Time) error

	// Catalog and ACLs.
	CreateLibrary(ctx context.Context, library Library) error
	LibraryByID(ctx context.Context, userID, libraryID string, required LibraryRole) (AccessibleLibrary, error)
	ListLibraries(ctx context.Context, userID string, required LibraryRole) ([]AccessibleLibrary, error)
	GrantLibraryAccess(ctx context.Context, actorUserID, libraryID, userID string, role LibraryRole, at time.Time) error
	RevokeLibraryAccess(ctx context.Context, actorUserID, libraryID, userID string) error
	CreateCatalogBook(ctx context.Context, actorUserID string, book CatalogBook) error
	CatalogBookByID(ctx context.Context, userID, bookID string, required LibraryRole) (CatalogBook, error)
	ListCatalogBooks(ctx context.Context, userID, libraryID string) ([]CatalogBook, error)
	// ResolveCatalogBookWork resolves the user's work graph and inserts the
	// catalog mapping in the same transaction. Low-confidence and conflicting
	// resolutions do not mutate the graph or create a mapping.
	ResolveCatalogBookWork(ctx context.Context, userID, bookID string, proposed Work, editions []Edition, ids []Identifier, confirmed bool, at time.Time) (WorkResolution, error)
	UserBookWork(ctx context.Context, userID, bookID string) (UserBookWork, error)

	// Ingestion jobs.
	CreateIngestJob(ctx context.Context, actorUserID string, request IngestJobRequest) (IngestJob, bool, error)
	IngestJobByID(ctx context.Context, actorUserID, jobID string) (IngestJob, error)
	ListIngestJobs(ctx context.Context, actorUserID, libraryID string, after *IngestJobCursor, limit int) ([]IngestJob, error)
	// TransitionIngestJob is an internal worker/upload operation scoped by the
	// initiating user, not by their current library ACL.
	TransitionIngestJob(ctx context.Context, userID, jobID string, transition IngestJobTransition) (IngestJob, error)

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

// CatalogWorkIdentifiers adds the stable catalog source alias while
// preserving the resolver's strongest-first identifier order.
func CatalogWorkIdentifiers(bookID string, ids []Identifier) []Identifier {
	stable := Identifier{Kind: "source", Value: "liseur-sync:" + bookID}
	out := make([]Identifier, 0, len(ids)+1)
	seen := make(map[string]bool, len(ids)+1)
	appendKind := func(kind string) {
		for _, id := range ids {
			key := id.Kind + ":" + id.Value
			if id.Kind == kind && !seen[key] {
				seen[key] = true
				out = append(out, id)
			}
		}
	}
	appendKind("sha256")
	appendKind("partial-md5")
	appendKind("source")
	stableKey := stable.Kind + ":" + stable.Value
	if !seen[stableKey] {
		seen[stableKey] = true
		out = append(out, stable)
	}
	appendKind("dc")
	appendKind("ta")
	for _, id := range ids {
		key := id.Kind + ":" + id.Value
		if !seen[key] {
			seen[key] = true
			out = append(out, id)
		}
	}
	return out
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
