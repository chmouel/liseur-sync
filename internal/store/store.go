// Package store defines the storage interface for liseur-sync, with
// SQLite and PostgreSQL backends. All queries are scoped by user_id;
// no caller constructs cross-user queries.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/contentpath"
)

// Common sentinel errors.
var (
	ErrNotFound            = errors.New("store: not found")
	ErrConflict            = errors.New("store: conflict") // uniqueness or state conflict
	ErrIDMismatch          = errors.New("store: idempotent id reused with different payload")
	ErrIdempotencyConflict = errors.New("store: idempotency key reused with different request")
	ErrInvalidTransition   = errors.New("store: invalid state transition")
	ErrStaleRevision       = errors.New("store: stale revision")
	ErrQuotaExceeded       = errors.New("store: quota exceeded")
	ErrContentMismatch     = errors.New("store: content mismatch")
	ErrPromotionConflict   = errors.New("store: promotion conflict")
	ErrInvariantViolation  = errors.New("store: invariant violation")
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

// Valid reports whether the source is one of the four precedence stages.
func (s MetadataSource) Valid() bool {
	return s == MetadataEmbedded || s == MetadataFilename ||
		s == MetadataExternal || s == MetadataManual
}

// MetadataSetLocks records the set-level manual locks of one book. A row
// lock cannot express a deliberately emptied set, because removing the last
// row leaves nothing behind to carry the lock, so the set-level flag is what
// keeps an emptied set empty across rescans.
type MetadataSetLocks struct {
	Identifiers  bool `json:"-"`
	Languages    bool `json:"-"`
	Tags         bool `json:"-"`
	Genres       bool `json:"-"`
	Series       bool `json:"-"`
	Contributors bool `json:"-"`
}

// CatalogBook is shared through a library ACL. Its sync Work remains
// user-scoped and is joined through UserBookWork.
//
// Revision and SetLocks are server-managed state rather than request
// payload, so they are excluded from the JSON encoding that
// NewBookPromotionFingerprint hashes; including them would change the
// fingerprint of every in-flight promotion across an upgrade.
type CatalogBook struct {
	ID        string
	LibraryID string
	Status    BookStatus
	Revision  int64            `json:"-"`
	SetLocks  MetadataSetLocks `json:"-"`
	// ReviewReason says why the book needs an administrator's attention.
	// It is set only alongside the review status and is empty otherwise,
	// so a book in review always explains itself: a review item nobody can
	// interpret is the same as no review item.
	ReviewReason        string
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

// BookIdentifier is one publication identifier row. Values are compared
// verbatim; only the scheme is folded, because identifier values are
// case-sensitive in general.
type BookIdentifier struct {
	Scheme string
	Value  string
	Source MetadataSource
	Locked bool
}

// BookLanguage is one language row of a book.
type BookLanguage struct {
	Language string
	Source   MetadataSource
	Locked   bool
}

// BookTaxon is one tag or genre membership. ID and NormalizedName identify
// the shared library-wide entity; Name is its display spelling.
type BookTaxon struct {
	ID             string
	Name           string
	NormalizedName string
	Source         MetadataSource
	Locked         bool
}

// BookSeries is one series membership. Position is absent when the source
// named a series without a place in it; a missing position is never
// invented.
type BookSeries struct {
	SeriesID       string
	Name           string
	NormalizedName string
	Position       *float64
	Source         MetadataSource
	Locked         bool
}

// BookContributor is one contributor in one role. A person credited twice
// in different roles is two rows over one contributor entity.
type BookContributor struct {
	ContributorID  string
	Name           string
	NormalizedName string
	Role           string
	Position       int
	Source         MetadataSource
	Locked         bool
}

// BookMetadata is one book's scalar fields together with every metadata
// entity set attached to it, read in one transaction so the precedence
// engine merges against a consistent snapshot. Rows come back in a
// deterministic order so repeated merges of the same proposal are
// reproducible.
type BookMetadata struct {
	Book         CatalogBook
	Identifiers  []BookIdentifier
	Languages    []BookLanguage
	Tags         []BookTaxon
	Genres       []BookTaxon
	Series       []BookSeries
	Contributors []BookContributor
}

// ApplyBookMetadataRequest replaces one book's scalar metadata fields and
// every metadata entity set attached to it. The caller resolves the new
// state first — the precedence engine lives outside the store — and the
// store writes it atomically under ExpectedRevision, so a concurrent writer
// loses with ErrStaleRevision rather than silently overwriting.
//
// Metadata carries the complete resolved sets, not a delta: rows the caller
// omits are removed. Entity rows are matched by normalized name within the
// library; the supplied ID is used only when no such entity exists yet, so
// ID generation stays at the edge like every other identifier in the store.
type ApplyBookMetadataRequest struct {
	Metadata         BookMetadata
	ExpectedRevision int64
	UpdatedAt        time.Time
}

// ValidateApplyBookMetadata checks the invariants a backend trusts. Handlers
// and workers call it at the edge; the store does not revalidate.
func ValidateApplyBookMetadata(request ApplyBookMetadataRequest) error {
	book := request.Metadata.Book
	if book.ID == "" || book.LibraryID == "" || request.ExpectedRevision < 1 ||
		request.UpdatedAt.IsZero() {
		return ErrInvalidTransition
	}
	for _, source := range []MetadataSource{
		book.TitleSource, book.SubtitleSource, book.DescriptionSource,
		book.PublisherSource, book.PublishedDateSource,
	} {
		if source != "" && !source.Valid() {
			return ErrInvalidTransition
		}
	}
	identifiers := make(map[IdentifierKey]struct{}, len(request.Metadata.Identifiers))
	for _, row := range request.Metadata.Identifiers {
		if row.Scheme == "" || row.Value == "" || !row.Source.Valid() {
			return ErrInvalidTransition
		}
		key := IdentifierKey{Scheme: row.Scheme, Value: row.Value}
		if _, duplicate := identifiers[key]; duplicate {
			return ErrInvalidTransition
		}
		identifiers[key] = struct{}{}
	}
	languages := make(map[string]struct{}, len(request.Metadata.Languages))
	for _, row := range request.Metadata.Languages {
		if row.Language == "" || !row.Source.Valid() {
			return ErrInvalidTransition
		}
		if _, duplicate := languages[row.Language]; duplicate {
			return ErrInvalidTransition
		}
		languages[row.Language] = struct{}{}
	}
	for _, set := range [][]BookTaxon{request.Metadata.Tags, request.Metadata.Genres} {
		seen := make(map[string]struct{}, len(set))
		ids := make(map[string]struct{}, len(set))
		for _, row := range set {
			if row.ID == "" || row.Name == "" || row.NormalizedName == "" ||
				!row.Source.Valid() {
				return ErrInvalidTransition
			}
			if _, duplicate := seen[row.NormalizedName]; duplicate {
				return ErrInvalidTransition
			}
			// Entity ids are unique table-wide, so two rows offering the
			// same candidate id for different names would reach the store
			// as a raw constraint violation instead of a clean rejection.
			if _, duplicate := ids[row.ID]; duplicate {
				return ErrInvalidTransition
			}
			seen[row.NormalizedName] = struct{}{}
			ids[row.ID] = struct{}{}
		}
	}
	series := make(map[string]struct{}, len(request.Metadata.Series))
	seriesIDs := make(map[string]struct{}, len(request.Metadata.Series))
	for _, row := range request.Metadata.Series {
		if row.SeriesID == "" || row.Name == "" || row.NormalizedName == "" ||
			!row.Source.Valid() {
			return ErrInvalidTransition
		}
		if _, duplicate := series[row.NormalizedName]; duplicate {
			return ErrInvalidTransition
		}
		if _, duplicate := seriesIDs[row.SeriesID]; duplicate {
			return ErrInvalidTransition
		}
		series[row.NormalizedName] = struct{}{}
		seriesIDs[row.SeriesID] = struct{}{}
	}
	contributors := make(map[ContributorRoleKey]struct{}, len(request.Metadata.Contributors))
	contributorIDs := make(map[string]string, len(request.Metadata.Contributors))
	for _, row := range request.Metadata.Contributors {
		if row.ContributorID == "" || row.Name == "" || row.NormalizedName == "" ||
			row.Role == "" || row.Position < 0 || !row.Source.Valid() {
			return ErrInvalidTransition
		}
		key := ContributorRoleKey{NormalizedName: row.NormalizedName, Role: row.Role}
		if _, duplicate := contributors[key]; duplicate {
			return ErrInvalidTransition
		}
		// One contributor credited in several roles is several rows over one
		// entity, so an id may repeat only for its own normalized name.
		if name, seen := contributorIDs[row.ContributorID]; seen &&
			name != row.NormalizedName {
			return ErrInvalidTransition
		}
		contributors[key] = struct{}{}
		contributorIDs[row.ContributorID] = row.NormalizedName
	}
	return nil
}

// IdentifierKey and ContributorRoleKey are the composite primary keys of the
// identifier and contributor set rows, used to reject duplicates before the
// store has to.
type IdentifierKey struct {
	Scheme string
	Value  string
}

type ContributorRoleKey struct {
	NormalizedName string
	Role           string
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
		return to == IngestFailed
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
	ID                            string
	UserID                        string
	LibraryID                     string
	QuotaUserID                   string
	Source                        IngestSource
	ClientKey                     *string
	RequestFingerprint            string
	PromotionFingerprint          *string
	ArtifactsExpired              bool
	ArtifactCleanupPending        bool
	State                         IngestState
	BytesReceived                 int64
	ContentSHA256                 *string
	StagingPath                   *string
	SourceRelativePath            *string
	ExtractedEmbeddedMetadataJSON []byte
	BookID                        *string
	ErrorCode                     *string
	ErrorDetail                   *string
	RetryCount                    int64
	Revision                      int64
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
	ExpiresAt                     *time.Time
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

// IngestRecoveryCursor is the exclusive cursor for global recovery scans.
// Recovery is an internal housekeeping operation, not an authorization
// surface.
type IngestRecoveryCursor struct {
	UpdatedAt time.Time
	ID        string
}

// IngestJobTransition applies one revision-checked state change.
// ExtractedEmbeddedMetadataJSON is required as a valid JSON object and may only
// be assigned by a validated-to-extracted transition; every other transition
// preserves the existing snapshot and must leave this field empty. Error
// fields are required for failed/quarantined targets.
type IngestJobTransition struct {
	ExpectedState                 IngestState
	ExpectedRevision              int64
	NextState                     IngestState
	ExtractedEmbeddedMetadataJSON []byte
	ErrorCode                     string
	ErrorDetail                   string
	ExpiresAt                     *time.Time
	IncrementRetry                bool
	UpdatedAt                     time.Time
}

// BlobInfo identifies one verified durable CAS object.
type BlobInfo struct {
	SHA256    string
	SizeBytes int64
}

// BlobRecord is the database reconciliation state for one CAS object.
type BlobRecord struct {
	BlobInfo
	OrphanedAt *time.Time
	MissingAt  *time.Time
}

// BlobReconcileResult describes state changes from one filesystem
// observation. Reconciliation only marks; physical deletion is a later
// grace-period sweep.
type BlobReconcileResult struct {
	Record         BlobRecord
	Inserted       bool
	OrphanMarked   bool
	OrphanCleared  bool
	MissingMarked  bool
	MissingCleared bool
}

// CatalogAvailabilityResult counts one bounded catalog availability pass.
// Files follow their blob: a file whose bytes are gone is not downloadable,
// and a book with no downloadable file is not a book a reader can open.
type CatalogAvailabilityResult struct {
	FilesMarkedMissing   int
	FilesMarkedAvailable int
	BooksMarkedMissing   int
	BooksMarkedActive    int
}

// Changed reports whether the pass mutated anything, so that a caller can
// stop looping.
func (r CatalogAvailabilityResult) Changed() bool {
	return r.FilesMarkedMissing != 0 || r.FilesMarkedAvailable != 0 ||
		r.BooksMarkedMissing != 0 || r.BooksMarkedActive != 0
}

// WatchedFile is what a sweep already knows about one watched source path:
// the book it belongs to, the snapshot taken from it, and what the last
// sweep observed. It is not a BookFile because a sweep needs the blob's
// recorded size to decide whether a path is worth rehashing, and that lives
// on the blob rather than the file.
type WatchedFile struct {
	FileID             string
	LibraryID          string
	BookID             string
	BookStatus         BookStatus
	SourceRelativePath string
	BlobSHA256         string
	// SizeBytes is the snapshot's size, which is also the size the source
	// had when it was taken.
	SizeBytes int64
	// SourceModifiedAt is the modification time the source carried when it
	// was last reconciled, or nil for a file no sweep has stat'ed yet.
	SourceModifiedAt *time.Time
	Availability     BookFileAvailability
	SourceAbsent     bool
}

// WatchedObservation is one path a completed traversal found, as the
// filesystem described it.
type WatchedObservation struct {
	SourceRelativePath string
	SizeBytes          int64
	ModifiedAt         time.Time
}

// MaxWatchedObservationBatch bounds one MarkWatchedSourcesSeen call. A
// sweep of any size is expressed as several batches, so one enormous
// library cannot build a single statement without bound.
const MaxWatchedObservationBatch = 500

// ValidateWatchedObservations checks a seen-batch before any backend
// builds a statement from it.
func ValidateWatchedObservations(paths []WatchedObservation) error {
	if len(paths) == 0 || len(paths) > MaxWatchedObservationBatch {
		return ErrInvalidTransition
	}
	for _, p := range paths {
		if p.SourceRelativePath == "" ||
			len(p.SourceRelativePath) > 4096 ||
			p.SizeBytes < 0 || p.ModifiedAt.IsZero() {
			return ErrInvalidTransition
		}
	}
	return nil
}

// TrashPurgeResult counts one bounded permanent-deletion pass.
type TrashPurgeResult struct {
	BookIDs []string
	// FilesPurged counts catalog references removed, not blobs: several
	// references can share one deduplicated blob.
	FilesPurged int
	// ReservationsReleased counts quota charges returned to a principal,
	// which happens only when that principal's last reference to a blob
	// goes away.
	ReservationsReleased int
	// BlobsOrphaned counts blobs that lost their last reference and so
	// became eligible for the separate orphan grace period.
	BlobsOrphaned int
}

// QuotaUsage is the logical per-principal usage after an operation.
type QuotaUsage struct {
	UsedBytes       int64
	AdditionalBytes int64
}

// QuotaExceededError reports an atomic quota rejection.
type QuotaExceededError struct {
	LimitBytes      int64
	UsedBytes       int64
	AdditionalBytes int64
}

func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf(
		"%v: limit=%d used=%d additional=%d",
		ErrQuotaExceeded, e.LimitBytes, e.UsedBytes, e.AdditionalBytes)
}

func (e *QuotaExceededError) Unwrap() error {
	return ErrQuotaExceeded
}

// CommitIngestStageRequest records a durable filesystem stage and its
// transient logical quota hold in one transaction.
type CommitIngestStageRequest struct {
	ExpectedRevision int64
	Artifact         BlobInfo
	StagingPath      string
	QuotaLimitBytes  *int64
	UpdatedAt        time.Time
}

type CommitIngestStageResult struct {
	Job   IngestJob
	Quota QuotaUsage
}

// BookFileAvailability is the current catalog visibility of one file.
type BookFileAvailability string

const (
	BookFileAvailable  BookFileAvailability = "available"
	BookFileMissing    BookFileAvailability = "missing"
	BookFileSuperseded BookFileAvailability = "superseded"
)

func (a BookFileAvailability) Valid() bool {
	return a == BookFileAvailable || a == BookFileMissing ||
		a == BookFileSuperseded
}

// BookFile is one immutable blob reference in a catalog book.
type BookFile struct {
	ID                 string
	LibraryID          string
	BookID             string
	BlobSHA256         string
	Source             IngestSource
	SourceRelativePath *string
	OriginalFilename   string
	MediaType          string
	PartialMD5         *string
	DCIdentifier       *string
	Availability       BookFileAvailability
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CatalogBookCursor is the exclusive cursor for catalog listings. It pairs
// the sort key with the id so that books created in the same instant still
// page deterministically.
type CatalogBookCursor struct {
	CreatedAt time.Time
	ID        string
}

// DuplicateContentBook is one catalog book that shares its bytes with
// another book in the same library. SHA256 is what they have in common
// and is what groups them: books carrying the same digest are the same
// file, whatever their titles say.
type DuplicateContentBook struct {
	Book   CatalogBook
	SHA256 string
}

// EntityKind names one of the four library-wide metadata entity tables.
// It is a closed set because it selects a table: a caller cannot ask for
// a kind the schema does not have, and the store never interpolates a
// caller's string into SQL.
type EntityKind string

const (
	EntitySeries      EntityKind = "series"
	EntityContributor EntityKind = "contributor"
	EntityTag         EntityKind = "tag"
	EntityGenre       EntityKind = "genre"
)

// Valid reports whether the kind names a table the store knows.
func (k EntityKind) Valid() bool {
	return k == EntitySeries || k == EntityContributor ||
		k == EntityTag || k == EntityGenre
}

// CatalogEntity is one library-wide series, contributor, tag or genre,
// together with how many of the library's active books claim it.
//
// The count is what makes the list usable: an entity with one book is
// usually a typo of one with forty, and that is exactly the pair somebody
// is looking for when they open a merge tool.
type CatalogEntity struct {
	ID             string
	Kind           EntityKind
	Name           string
	NormalizedName string
	BookCount      int
	CreatedAt      time.Time
}

// MaxEntityListLimit bounds one entity listing. Entity lists are browsed
// by a person, and a library with more distinct tags than this has a
// problem no page size can fix.
const MaxEntityListLimit = 500

// SearchQuery asks one library for the books matching some words.
//
// It carries no reading-state filter, and that is a rule rather than an
// omission (ADR-0004): a catalog-only credential must not be able to
// observe reading state, and the surest way to keep that true is for the
// catalog's search to have no vocabulary for it.
type SearchQuery struct {
	LibraryID string
	// Text is what the person typed. It is treated as words to match,
	// never as index syntax, so no character in it can change how the
	// query is read.
	Text string
	// Entities narrows to books claiming all of these, whatever their
	// kind. Facets are how a caller learns which ids are worth sending.
	Entities []string
	Limit    int
}

// MaxSearchLimit bounds one search. Search answers "where is that book",
// which is a question with a short answer; browsing a whole library is
// what the paged listings are for.
const MaxSearchLimit = 100

// MaxSearchFacets bounds how many values of each kind a result describes.
// A facet list is a set of suggestions, and a suggestion nobody will read
// is only a bigger response.
const MaxSearchFacets = 20

// SearchResult is one library's answer: the matching books, best first,
// and what those books have in common.
type SearchResult struct {
	Books  []CatalogBook
	Facets []SearchFacet
	// Truncated says the answer was cut at the limit, so a caller can
	// say "narrow this" rather than implying it found everything.
	Truncated bool
}

// SearchFacet is one entity the matching books share, with how many of
// them claim it. Counts are over the matched set rather than the library,
// because a facet's job is to describe the answer.
type SearchFacet struct {
	Kind      EntityKind
	ID        string
	Name      string
	BookCount int
}

// CommitNewBookPromotionRequest atomically creates one new catalog book and
// file from an extracted job after its CAS blob is durable.
type CommitNewBookPromotionRequest struct {
	ExpectedRevision int64
	Blob             BlobInfo
	Book             CatalogBook
	File             BookFile
	UpdatedAt        time.Time
}

type IngestPromotionResult struct {
	Job      IngestJob
	Book     CatalogBook
	File     BookFile
	Blob     BlobInfo
	Replayed bool
}

// NewBookPromotionFingerprint identifies the immutable request payload that
// produced a promoted job. ExpectedRevision is intentionally excluded so a
// caller can replay after losing the successful response.
func NewBookPromotionFingerprint(request CommitNewBookPromotionRequest) (string, error) {
	normalizeTime := func(value time.Time) time.Time {
		return value.UTC().Truncate(time.Microsecond)
	}
	normalizeTimePtr := func(value *time.Time) *time.Time {
		if value == nil {
			return nil
		}
		normalized := normalizeTime(*value)
		return &normalized
	}
	request.UpdatedAt = normalizeTime(request.UpdatedAt)
	request.Book.CreatedAt = normalizeTime(request.Book.CreatedAt)
	if request.Book.UpdatedAt.IsZero() {
		request.Book.UpdatedAt = request.Book.CreatedAt
	} else {
		request.Book.UpdatedAt = normalizeTime(request.Book.UpdatedAt)
	}
	request.Book.TrashedAt = normalizeTimePtr(request.Book.TrashedAt)
	request.Book.TrashExpiresAt = normalizeTimePtr(request.Book.TrashExpiresAt)
	request.File.CreatedAt = normalizeTime(request.File.CreatedAt)
	request.File.UpdatedAt = normalizeTime(request.File.UpdatedAt)
	if request.File.MediaType == "" {
		request.File.MediaType = "application/epub+zip"
	}
	payload, err := json.Marshal(struct {
		Blob      BlobInfo    `json:"blob"`
		Book      CatalogBook `json:"book"`
		File      BookFile    `json:"file"`
		UpdatedAt time.Time   `json:"updated_at"`
	}{
		Blob: request.Blob, Book: request.Book, File: request.File,
		UpdatedAt: request.UpdatedAt,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func ValidateBlobInfo(blob BlobInfo) error {
	if blob.SizeBytes < 0 || len(blob.SHA256) != sha256.Size*2 {
		return ErrContentMismatch
	}
	decoded, err := hex.DecodeString(blob.SHA256)
	if err != nil || hex.EncodeToString(decoded) != blob.SHA256 {
		return ErrContentMismatch
	}
	return nil
}

func ValidateCommitIngestStage(jobID string, request CommitIngestStageRequest) error {
	if jobID == "" || request.ExpectedRevision < 1 || request.UpdatedAt.IsZero() ||
		request.StagingPath == "" || len(request.StagingPath) > 4096 {
		return ErrInvalidTransition
	}
	if request.StagingPath != contentpath.StagingPath(jobID) {
		return ErrInvalidTransition
	}
	if err := ValidateBlobInfo(request.Artifact); err != nil {
		return err
	}
	if request.QuotaLimitBytes != nil && *request.QuotaLimitBytes < 0 {
		return ErrInvalidTransition
	}
	return nil
}

func ValidateNewBookPromotion(request CommitNewBookPromotionRequest) error {
	if request.ExpectedRevision < 1 || request.UpdatedAt.IsZero() {
		return ErrInvalidTransition
	}
	if err := ValidateBlobInfo(request.Blob); err != nil {
		return err
	}
	if request.Book.ID == "" || request.Book.LibraryID == "" ||
		request.Book.Status != BookActive || request.Book.CreatedAt.IsZero() {
		return ErrInvalidTransition
	}
	if request.Book.UpdatedAt.IsZero() {
		request.Book.UpdatedAt = request.Book.CreatedAt
	}
	file := request.File
	if file.ID == "" || file.LibraryID == "" || file.BookID == "" ||
		file.BlobSHA256 == "" || !file.Source.Valid() ||
		!file.Availability.Valid() || file.Availability != BookFileAvailable ||
		file.CreatedAt.IsZero() || file.UpdatedAt.IsZero() {
		return ErrInvalidTransition
	}
	if file.Source == IngestUpload && file.SourceRelativePath != nil {
		return ErrInvalidTransition
	}
	if file.Source == IngestWatched &&
		(file.SourceRelativePath == nil || *file.SourceRelativePath == "") {
		return ErrInvalidTransition
	}
	return nil
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
	if current.ArtifactsExpired {
		return IngestJob{}, ErrInvalidTransition
	}
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
	extracting := change.ExpectedState == IngestValidated &&
		change.NextState == IngestExtracted
	if extracting {
		metadataJSON := bytes.TrimSpace(
			change.ExtractedEmbeddedMetadataJSON)
		if len(metadataJSON) < 2 || metadataJSON[0] != '{' ||
			metadataJSON[len(metadataJSON)-1] != '}' ||
			!json.Valid(metadataJSON) {
			return IngestJob{}, ErrInvalidTransition
		}
	} else if len(change.ExtractedEmbeddedMetadataJSON) != 0 {
		return IngestJob{}, ErrInvalidTransition
	}
	retrying := change.ExpectedState == IngestFailed ||
		change.ExpectedState == IngestQuarantined
	if change.IncrementRetry != retrying {
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
	if extracting {
		next.ExtractedEmbeddedMetadataJSON =
			append([]byte(nil), change.ExtractedEmbeddedMetadataJSON...)
	}
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
	// SetLibraryConfig replaces one library's configuration document under
	// the manage role. The store does not interpret the document: it is
	// opaque here and parsed by whichever package owns each key, so a
	// setting added later needs no schema change and no store change.
	SetLibraryConfig(ctx context.Context, actorUserID, libraryID string, configJSON []byte, at time.Time) error
	GrantLibraryAccess(ctx context.Context, actorUserID, libraryID, userID string, role LibraryRole, at time.Time) error
	RevokeLibraryAccess(ctx context.Context, actorUserID, libraryID, userID string) error
	CreateCatalogBook(ctx context.Context, actorUserID string, book CatalogBook) error
	// CatalogBookByID reads one book the caller may see. Trashed books are
	// not visible through it at any role: a deleted book is deleted as far
	// as every reader is concerned, and the way back is ListTrashedBooks
	// plus RestoreCatalogBook, not an ordinary catalog read.
	CatalogBookByID(ctx context.Context, userID, bookID string, required LibraryRole) (CatalogBook, error)
	// ListTrashedBooks lists one library's trashed books, most recently
	// trashed first, under the manage role. It is what makes deletion
	// reversible in practice: without a way to see the trash, the
	// retention window is only a delay.
	ListTrashedBooks(ctx context.Context, userID, libraryID string, limit int) ([]CatalogBook, error)
	// ListDuplicateContentBooks lists the library's active books whose
	// bytes another active book in the same library also has, ordered so
	// that the ones sharing a digest arrive together.
	//
	// It reports and never merges. Two catalog entries for one blob are a
	// thing a user may have meant — the same file filed twice on purpose
	// — so the server's job is to make the coincidence visible, not to
	// pick which entry survives. Read access is enough to see it;
	// resolving it is an ordinary deletion, which is not.
	ListDuplicateContentBooks(ctx context.Context, userID, libraryID string, limit int) ([]DuplicateContentBook, error)
	// ListSimilarBooks reports the weaker kind of duplicate: books that
	// look like one book without being one file. It is a question put to
	// a librarian rather than a finding, and the rule it applies is
	// GroupSimilarBooks, shared so both backends answer alike.
	ListSimilarBooks(ctx context.Context, userID, libraryID string, limit int) ([]SimilarBookGroup, error)
	// ListCatalogBooks pages one library's readable books, oldest first.
	// Trashed books are excluded: they are not part of the catalog a
	// reader browses. A nil cursor starts at the beginning.
	ListCatalogBooks(ctx context.Context, userID, libraryID string, after *CatalogBookCursor, limit int) ([]CatalogBook, error)
	// ListRecentCatalogBooks pages the same books newest first. It is a
	// separate method rather than a direction flag because the cursor
	// means the opposite thing: `before` is where the last page ended
	// going down, and a caller that mixed the two would silently page
	// through a different order than it asked for.
	ListRecentCatalogBooks(ctx context.Context, userID, libraryID string, before *CatalogBookCursor, limit int) ([]CatalogBook, error)
	// ListBookFiles returns one book's files, newest first, so that a
	// download can pick the current one. It requires read access to the
	// book's library. Trashed books keep their files, because that is
	// what makes restore a relink; deciding whether they may be served is
	// the catalog's job, not this one's.
	ListBookFiles(ctx context.Context, userID, bookID string, required LibraryRole) ([]BookFile, error)
	// CatalogBookMetadata reads one book's scalar fields and every metadata
	// entity set attached to it in a single transaction, so a caller can run
	// the precedence engine against a consistent snapshot.
	CatalogBookMetadata(ctx context.Context, userID, bookID string, required LibraryRole) (BookMetadata, error)
	// ApplyCatalogBookMetadata atomically replaces one book's resolved
	// metadata under an expected revision. It requires the manage role and
	// returns ErrStaleRevision when another writer got there first.
	ApplyCatalogBookMetadata(ctx context.Context, userID string, request ApplyBookMetadataRequest) (BookMetadata, error)
	// ListCatalogEntities pages one library's entities of one kind by
	// name, with the number of active books claiming each. Read access is
	// enough: an entity list is how a reader browses a library.
	//
	// after is the normalized name to resume from, exclusive, so paging
	// is stable while books are added — an offset would skip or repeat
	// entities as counts change underneath it.
	ListCatalogEntities(ctx context.Context, userID, libraryID string, kind EntityKind, after string, limit int) ([]CatalogEntity, error)
	// CatalogEntityByID reads one entity under read access, so a page can
	// name what it is showing without listing the whole library.
	CatalogEntityByID(ctx context.Context, userID, libraryID, entityID string, kind EntityKind) (CatalogEntity, error)
	// ListBooksByEntity pages the library's active books claiming one
	// entity, oldest first, under read access. Trashed books are
	// excluded for the same reason ListCatalogBooks excludes them.
	ListBooksByEntity(ctx context.Context, userID, libraryID, entityID string, kind EntityKind, after *CatalogBookCursor, limit int) ([]CatalogBook, error)
	// RenameCatalogEntity changes one entity's display spelling under the
	// manage role. It returns ErrConflict when another entity of the same
	// kind already holds the new normalized name, because that is a merge
	// — folding two entities into one is a decision about identity, and
	// silently doing it under the name of a rename would hide it.
	RenameCatalogEntity(ctx context.Context, userID, libraryID, entityID string, kind EntityKind, name string) (CatalogEntity, error)
	// MergeCatalogEntities folds one entity into another of the same kind
	// in the same library, under the manage role, and reports how many
	// book memberships moved.
	//
	// Merges are explicit by design (ADR-0004): normalization matches
	// case and spacing only, so "Le Guin, Ursula K." and "Ursula K. Le
	// Guin" are two entities until somebody says otherwise, and only a
	// person can say it.
	MergeCatalogEntities(ctx context.Context, userID, libraryID, fromID, intoID string, kind EntityKind, at time.Time) (int, error)
	// SearchCatalogBooks finds one library's active books by words, under
	// read access, and describes what the matches have in common.
	//
	// The index is maintained inside the same transaction as every write
	// that changes what a book says, so a book is findable by its new
	// title the moment the edit that gave it one commits — a search that
	// lags behind the catalog is a search that lies about it.
	SearchCatalogBooks(ctx context.Context, userID string, query SearchQuery) (SearchResult, error)
	// ResolveCatalogBookWork resolves the user's work graph and inserts the
	// catalog mapping in the same transaction. Low-confidence and conflicting
	// resolutions do not mutate the graph or create a mapping.
	ResolveCatalogBookWork(ctx context.Context, userID, bookID string, proposed Work, editions []Edition, ids []Identifier, confirmed bool, at time.Time) (WorkResolution, error)
	UserBookWork(ctx context.Context, userID, bookID string) (UserBookWork, error)
	WorkBookIDs(ctx context.Context, userID string) (map[string]string, error)
	CatalogAuthorsForBooks(ctx context.Context, userID string, bookIDs []string) (map[string]string, error)
	// AvailableBookMediaTypes reports, per book, the media types the user
	// can actually be served right now — files that are present, in a
	// library they may read. It exists so a list of books can be told
	// which of them the browser could open without a query per row.
	AvailableBookMediaTypes(ctx context.Context, userID string, bookIDs []string) (map[string][]string, error)

	// Ingestion jobs.
	CreateIngestJob(ctx context.Context, actorUserID string, request IngestJobRequest) (IngestJob, bool, error)
	IngestJobByID(ctx context.Context, actorUserID, jobID string) (IngestJob, error)
	ListIngestJobs(ctx context.Context, actorUserID, libraryID string, after *IngestJobCursor, limit int) ([]IngestJob, error)
	// ListIngestActivity returns the jobs in one library that have not
	// reached the catalog — still in flight, quarantined or failed —
	// newest first. ListIngestJobs is the paginated audit view and runs
	// oldest-first, which is the wrong end for answering "what happened
	// to the file I just uploaded": promoted jobs are the bulk, and a
	// failure would sit pages deep. Manage role, like ListIngestJobs.
	ListIngestActivity(ctx context.Context, actorUserID, libraryID string, limit int) ([]IngestJob, error)
	// ListIngestRecoveryJobs is a global housekeeping query for stale jobs
	// whose durable artifacts must be verified before workers resume them.
	ListIngestRecoveryJobs(ctx context.Context, before time.Time, after *IngestRecoveryCursor, limit int) ([]IngestJob, error)
	// ListAbandonedIngestJobs pages the jobs still in `received`, oldest
	// first, after the id given. A job leaves that state as soon as its
	// bytes are committed, so one that is still in it either has an
	// upload in flight or was interrupted by a crash between writing the
	// bytes and recording them. Only the caller can tell those apart —
	// at startup nothing is in flight, so all of them are the second.
	ListAbandonedIngestJobs(ctx context.Context, afterID string, limit int) ([]IngestJob, error)
	// ListIngestWorkerJobs snapshots one bounded internal worker batch.
	// Revision-checked transitions resolve concurrent workers.
	ListIngestWorkerJobs(ctx context.Context, state IngestState, limit int) ([]IngestJob, error)
	CommitIngestStage(ctx context.Context, userID, jobID string, request CommitIngestStageRequest) (CommitIngestStageResult, error)
	CommitNewBookPromotion(ctx context.Context, userID, jobID string, request CommitNewBookPromotionRequest) (IngestPromotionResult, error)
	// ListBlobRecords and ReconcileBlob are global housekeeping operations.
	ListBlobRecords(ctx context.Context, afterSHA256 string, limit int) ([]BlobRecord, error)
	// ListReferencedBlobs pages the blobs the database says must exist,
	// ordered by digest. It is what makes a backup checkable: a backup is
	// only valid if every referenced blob is in it. Trashed books count,
	// because their files are what a restore relinks; unreferenced blobs
	// do not, because they are the orphan sweep's business.
	ListReferencedBlobs(ctx context.Context, afterSHA256 string, limit int) ([]BlobInfo, error)
	ReconcileBlob(ctx context.Context, blob BlobInfo, present bool, at time.Time) (BlobReconcileResult, error)
	// ReconcileCatalogAvailability is a global housekeeping operation that
	// propagates blob presence into the catalog: a file whose blob is
	// recorded missing becomes unavailable, and one whose blob has returned
	// becomes available again. Superseded files are left alone, because
	// supersession is a different axis from presence. Books follow their
	// files between active and missing; trashed and review books are never
	// touched, since neither state is a statement about bytes. The limit
	// bounds each of the four updates, so a caller loops while Changed
	// reports work was done.
	ReconcileCatalogAvailability(ctx context.Context, at time.Time, limit int) (CatalogAvailabilityResult, error)
	// ListWatchedLibraries is a global housekeeping query. The scanner
	// runs on the server's behalf rather than any user's, so it is one of
	// the documented cross-user lookups: it exists for a background job,
	// and nothing user-facing may call it. Managed libraries are excluded
	// because they have no root to sweep.
	ListWatchedLibraries(ctx context.Context) ([]Library, error)
	// WatchedFilesByPath reads what the catalog already holds for one
	// watched source path. It is a global housekeeping query like the
	// other reconciliation methods — a sweep runs on the server's behalf,
	// not a user's — but it is still scoped to one library, so it can
	// never report a path from a library the sweep was not asked about.
	//
	// It returns every match rather than one. Nothing stops two books
	// referencing the same path, and a sweep that saw only the first would
	// silently pick a winner; the ADR's rule is that ambiguity is
	// reported, not resolved.
	WatchedFilesByPath(ctx context.Context, libraryID, sourceRelativePath string) ([]WatchedFile, error)
	// MarkWatchedSourcesSeen records that a traversal found these paths,
	// with the size and modification time the filesystem reported. Seeing
	// a path clears any absence recorded for it, which is what makes a
	// returning file available again.
	//
	// Only the observation is written. Whether the bytes still match the
	// snapshot is the caller's judgement, because answering it can cost a
	// full read.
	MarkWatchedSourcesSeen(ctx context.Context, libraryID string, paths []WatchedObservation, at time.Time) (int, error)
	// MarkWatchedSourcesAbsent records that a **completed** full sweep of
	// one library did not find these files' paths. It selects by absence
	// of evidence — every watched file in the library not seen since the
	// sweep began — so the caller must never call it after a traversal
	// that ended early, and the store cannot check that for it.
	//
	// Files created after the sweep began are exempt. Such a row was
	// promoted from content this sweep or a later one discovered, so the
	// sweep never proved anything about it.
	MarkWatchedSourcesAbsent(ctx context.Context, libraryID string, sweepStartedAt, at time.Time, limit int) (int, error)
	// SetCatalogBookReview moves one book into review with the reason
	// given, or, with an empty reason, out of review and back to missing —
	// from where ReconcileCatalogAvailability restores it to active if it
	// still has a servable file. It reports whether it changed anything.
	//
	// Review is deliberately not reversible into `active` here. Deciding a
	// book is servable is that one pass's job, and a second writer with
	// its own opinion is how the two end up disagreeing.
	SetCatalogBookReview(ctx context.Context, libraryID, bookID, reason string, at time.Time) (bool, error)
	// ListBooksInReview pages one library's books awaiting an
	// administrator's decision, oldest first, under the manage role.
	// Without it the review status is a dead end rather than a queue.
	ListBooksInReview(ctx context.Context, userID, libraryID string, limit int) ([]CatalogBook, error)
	// TrashCatalogBook moves one book out of the catalog under the manage
	// role. Its files are retained, so the blobs stay referenced, stay GC
	// roots, and keep counting against quota: that is what stops an
	// upload/delete cycle from growing the disk without bound, and what
	// makes restore a relink rather than a re-upload.
	TrashCatalogBook(ctx context.Context, userID, bookID string, at, expiresAt time.Time) (CatalogBook, error)
	// RestoreCatalogBook returns a trashed book to the catalog. It restores
	// to missing rather than active when the book has no servable file, so
	// restore cannot advertise a download that is not there.
	RestoreCatalogBook(ctx context.Context, userID, bookID string, at time.Time) (CatalogBook, error)
	// PurgeExpiredTrash is a global housekeeping operation. It permanently
	// removes books whose trash retention has passed, releases the quota
	// reservations that lose their last reference, and orphan-marks blobs
	// that lose their last reference so the existing grace-period sweep
	// reclaims the bytes. It never deletes content itself.
	PurgeExpiredTrash(ctx context.Context, before time.Time, limit int) (TrashPurgeResult, error)
	// PurgeOrphanedBlobRecords atomically removes database rows that have
	// remained orphaned through the supplied cutoff and still have no retained
	// book-file references or active ingest holds. The caller must keep content
	// writers paused until it removes the returned physical blobs idempotently;
	// a cleanup failure is rediscovered and re-marked by the next filesystem
	// reconciliation pass.
	PurgeOrphanedBlobRecords(ctx context.Context, before time.Time, limit int) ([]BlobRecord, error)
	// PurgeExpiredIngestArtifacts is a global housekeeping operation. It
	// releases quota and returns staging paths for filesystem cleanup, while
	// retaining permanent job tombstones so deterministic paths cannot be
	// reused by a later job with the same ID.
	PurgeExpiredIngestArtifacts(ctx context.Context, before time.Time, limit int) ([]IngestJob, error)
	// CompleteIngestArtifactCleanup acknowledges idempotent filesystem removal
	// and clears the retained artifact identity from its terminal tombstone.
	CompleteIngestArtifactCleanup(ctx context.Context, jobID, stagingPath string) error
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
