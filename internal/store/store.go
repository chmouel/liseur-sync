// Package store defines the storage interface for liseur-sync, with
// SQLite and PostgreSQL backends.
//
// Per-user reading state — ops, sessions, works, editions, aliases and
// the user_book_works bridge — is scoped by user_id in every query, and
// no caller constructs a cross-user one. The catalog is deliberately
// not: ADR-0017 makes every folder's books visible to every signed-in
// account, so catalog reads take a folder id and no principal. What
// stays private is what somebody read, never what the server holds.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Common sentinel errors.
var (
	ErrNotFound            = errors.New("store: not found")
	ErrInvalidInput        = errors.New("store: invalid input")
	ErrConflict            = errors.New("store: conflict") // uniqueness or state conflict
	ErrIDMismatch          = errors.New("store: idempotent id reused with different payload")
	ErrIdempotencyConflict = errors.New("store: idempotency key reused with different request")
	ErrInvalidTransition   = errors.New("store: invalid state transition")
	ErrStaleRevision       = errors.New("store: stale revision")
	ErrQuotaExceeded       = errors.New("store: quota exceeded")
	ErrContentMismatch     = errors.New("store: content mismatch")
	ErrPromotionConflict   = errors.New("store: promotion conflict")
	ErrInvariantViolation  = errors.New("store: invariant violation")
	// ErrLastAdmin refuses the demotion or disabling that would leave
	// the instance with no enabled administrator. The CLI is the
	// recovery path, so the panel must never be able to close it.
	ErrLastAdmin = errors.New("store: last enabled admin")
	// ErrAdminGrantRequiresAdmin refuses an admin-scoped token to an
	// account that is not an enabled admin. It is enforced inside the
	// transaction that writes the token, so it cannot race a demotion.
	ErrAdminGrantRequiresAdmin = errors.New("store: admin scope requires an admin account")
	// ErrRefreshLeaseLost refuses a write from a worker that no longer
	// holds the library it is refreshing: the lease expired and somebody
	// else took it, or a second worker was already there. The refresh is
	// convergent, so the answer is to stop, not to retry — the holder
	// finishes what this worker started.
	ErrRefreshLeaseLost = errors.New("store: refresh lease lost")
)

// TokenPurgeGrace is how long expired or revoked tokens remain listed
// (for the UI) before Housekeep deletes them.
const TokenPurgeGrace = 30 * 24 * time.Hour

// Scope is a token scope.
type Scope string

const (
	ScopeSync         Scope = "sync"
	ScopeReadInsights Scope = "read-insights"
	ScopeLibraryRead  Scope = "library-read"
	// ScopeLibraryManage permits stating series claims (ADR-0018). It
	// shapes how the catalog reads; it never writes to a watched folder.
	ScopeLibraryManage Scope = "library-manage"
	// ScopeLibraryUpload permits putting a book into a folder that
	// accepts uploads (ADR-0023). It is separate from library-manage
	// because tidying your own shelves and writing to the server's disk
	// are different questions, and one token should not answer both.
	ScopeLibraryUpload Scope = "library-upload"
	// ScopeLibraryDelete permits deleting a book out of a folder that
	// accepts uploads (ADR-0025). Separate from library-upload for the
	// reason that one is separate from library-manage: adding your own
	// book and removing one every account can see are different
	// questions. It bounds a token rather than dividing users — any
	// account may mint it — so its job is to stop a sync token from
	// deleting a library by accident or by compromise.
	ScopeLibraryDelete Scope = "library-delete"
	ScopeAdmin         Scope = "admin"
)

var scopeOrder = map[Scope]int{
	ScopeSync:          0,
	ScopeReadInsights:  1,
	ScopeLibraryRead:   2,
	ScopeLibraryManage: 3,
	ScopeLibraryUpload: 4,
	ScopeLibraryDelete: 5,
	ScopeAdmin:         6,
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

// Allows reports whether the set grants a requested capability. Admin
// implies everything; nothing else implies anything, since the catalog
// is read-only to every credential (ADR-0017).
func (s ScopeSet) Allows(scope Scope) bool {
	return s.Contains(ScopeAdmin) || s.Contains(scope)
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
	// IsAdmin is the single definition of an administrator (ADR-0013).
	// It is a property of the account, not of a credential: a token
	// carries capabilities, the account carries the role.
	IsAdmin bool
	// DisabledAt stops every credential the account holds. Nil is an
	// active account.
	DisabledAt *time.Time
	CreatedAt  time.Time
}

// Enabled reports whether the account may authenticate at all.
func (u User) Enabled() bool { return u.DisabledAt == nil }

// AdminCounts is the whole aggregate state the admin panel reports:
// integers and timestamps, no identifying strings (ADR-0013). It is one
// round trip because an overview that issues fifteen queries is an
// overview somebody eventually stops loading.
type AdminCounts struct {
	Users         int
	AdminUsers    int
	DisabledUsers int

	// Folders and FoldersByKind describe what the server is reflecting.
	// There is nothing to report about *how* books got there, because
	// there is no longer a pipeline they came through.
	Folders       int
	FoldersByKind map[string]int

	// BooksByStatus is keyed by the two values books.status allows:
	// active and missing. A run of missing books is the one catalog
	// number worth an administrator's attention, because it usually
	// means a disk is not where it was.
	BooksByStatus map[string]int
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

// ---------------------------------------------------------------------
// The catalog
// ---------------------------------------------------------------------

// FolderKind decides how a folder's books are discovered and where their
// metadata comes from.
//
// The two kinds are not two configurations of one scanner; they key
// books differently, and that difference is load-bearing (ADR-0017). A
// plain folder is a tree of files and a book is identified by where it
// sits. A Calibre folder is a database that happens to have files next
// to it, and a book is identified by its Calibre id — because Calibre
// rewrites a book's directory name whenever its title or author changes,
// and a server that keyed on the path would read every such edit as one
// book vanishing and another appearing, losing the reading position each
// time.
type FolderKind string

const (
	// FolderPlain is a directory tree that is walked for EPUBs.
	FolderPlain FolderKind = "plain"
	// FolderCalibre is a directory with a metadata.db at its root, read
	// as the curator's own catalog.
	FolderCalibre FolderKind = "calibre"
)

func (k FolderKind) Valid() bool {
	return k == FolderPlain || k == FolderCalibre
}

// Folder is a directory this server reflects.
//
// It has no owner, no quota principal and no access list. Every
// signed-in account sees every folder's books; only an administrator
// sees RootPath, which is a filesystem oracle and not a reader's
// business. Nothing beneath RootPath is written unless AcceptsUploads
// says somebody asked for it.
type Folder struct {
	ID       string
	Name     string
	RootPath string
	Kind     FolderKind
	// AcceptsUploads is the one place the server is allowed to write
	// under a folder root (ADR-0023, ADR-0025). It is false unless an
	// administrator set it, and it is the amendment to ADR-0017's rule
	// 3: writes happen only where somebody asked for them — creating a
	// file that was not there, or deleting one this server could have
	// created. Never modifying or renaming one.
	AcceptsUploads bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// DeleteBookOptions tunes deleting a catalog book (ADR-0025).
type DeleteBookOptions struct {
	// ForgetReadingFor is the reader whose work goes with the book, or
	// empty to leave every reader's reading behind. It is only ever one
	// reader — the one who asked — because a work is per-user and the
	// caller has no standing to forget anybody else's (ADR-0024).
	ForgetReadingFor string
}

// DeleteBookResult reports what happened to the caller's reading, which
// they cannot infer from success alone.
type DeleteBookResult struct {
	// ReadingForgotten is true when the caller's work went with the
	// book.
	ReadingForgotten bool
	// ReadingKept is true when forgetting was asked for and declined
	// because another catalog book still maps that work — a second copy
	// of the same book, which the reading now belongs to. Not a failure:
	// the reader asked to forget a book they still have.
	ReadingKept bool
}

// FolderCursor is the opaque pagination cursor of the folder list,
// ordered by name then id. The id comes first because it cannot contain
// a space and a folder name can contain anything, so cutting at the
// first space is unambiguous.
func FolderCursor(f Folder) string { return f.ID + " " + f.Name }

// SplitFolderCursor undoes FolderCursor. An unparseable cursor reads as
// "start from the beginning": it can only come from a URL somebody
// edited, and a first page is a better answer than a 400.
func SplitFolderCursor(c string) (name, id string) {
	id, name, _ = strings.Cut(c, " ")
	return name, id
}

// BookStatus is the catalog lifecycle state, and it has exactly two
// values. A book is either something a scan can see or something it
// could not find. There is no trash, because the server does not own
// the file and deleting one is not its business; there is no review
// queue, because nothing here asks a person a question.
type BookStatus string

const (
	BookActive  BookStatus = "active"
	BookMissing BookStatus = "missing"
)

func (s BookStatus) Valid() bool {
	return s == BookActive || s == BookMissing
}

// CatalogBook is one publication in one folder: one row, one file.
//
// The metadata carries no per-field source and no lock. Both existed to
// rank competing claims — a filename guess against an EPUB against a
// network lookup against a human edit — and with editing and external
// providers gone there is one source per folder kind and nothing to
// rank. A pass states what the folder says.
type CatalogBook struct {
	ID       string
	FolderID string
	Status   BookStatus

	// RelativePath is slash-separated and relative to the folder root,
	// so it is the same string on every platform and is what an open is
	// rooted at. It never escapes to a non-admin caller: joined to the
	// root it would name a path on the server's disk.
	RelativePath string
	SizeBytes    int64
	// MTime is the modification time the last scan saw. With SizeBytes
	// it is the change gate of a plain folder.
	MTime time.Time
	// ContentSHA256 is the publication's digest: what a client matches
	// its own copy against, and what the cover cache is keyed by.
	ContentSHA256    string
	OriginalFilename string
	MediaType        string
	// CalibreID identifies this book in its folder's metadata.db, and is
	// nil in a plain folder.
	CalibreID *int64
	// CoverRelativePath is a cover the curator chose, sitting beside the
	// publication rather than inside it — Calibre's cover.jpg.
	// CoverSHA256 proves it has not been replaced under a cache key that
	// names the old image.
	CoverRelativePath *string
	CoverSHA256       string

	Title         string
	Subtitle      string
	Description   string
	Publisher     string
	PublishedDate string

	CreatedAt time.Time
	UpdatedAt time.Time
	// SeenAt is when a scan last found this file, and AbsentAt when one
	// last proved it gone. Only a completed scan may set AbsentAt.
	SeenAt   *time.Time
	AbsentAt *time.Time
}

// BookIdentifier is one publication identifier. Values are compared
// verbatim; only the scheme is folded, because identifier values are
// case-sensitive in general.
type BookIdentifier struct {
	Scheme string
	Value  string
}

// BookTaxon is one tag membership. ID and NormalizedName identify the
// shared library-wide entity; Name is its display spelling.
type BookTaxon struct {
	ID             string
	Name           string
	NormalizedName string
}

// SeriesSource says which layer a membership came from (ADR-0018). A
// client shows it so a reader can tell what the folder said from what
// somebody claimed, and knows whether a reset is on offer.
type SeriesSource string

const (
	// SeriesSourceFolder is what the last reconcile pass observed.
	SeriesSourceFolder SeriesSource = "folder"
	// SeriesSourceShared is an administrator's claim, seen by everyone
	// who has not made a personal one.
	SeriesSourceShared SeriesSource = "shared"
	// SeriesSourcePersonal is one reader's own claim, seen by nobody
	// else.
	SeriesSourcePersonal SeriesSource = "personal"
)

// SharedSeriesScope is the scope_user value of the shared override
// layer. It is the empty string because no user id is empty, so the
// shared claim and a personal one can share a primary key without a
// nullable column, and the empty string sorts below every real id.
const SharedSeriesScope = ""

// NoReaderScope stands in for a caller with no user id when resolving
// series layers. It cannot be the empty string, which is the shared
// layer's own sentinel — using it would make every shared claim look
// personal — and it must match no account, so it is a value NewID
// cannot produce: ids are hex, and this is not.
//
// It is not a NUL byte, though that would also be unmintable: PostgreSQL
// text rejects NUL outright, and SQLite's tolerance of it made the
// difference invisible until the suite ran against both.
const NoReaderScope = "-"

// Writable reports whether a source names a layer somebody can claim.
// The folder layer is not one: it is what the disk said, and the only
// way to change it is to change the disk.
func (s SeriesSource) Writable() bool {
	return s == SeriesSourceShared || s == SeriesSourcePersonal
}

// ScopeUser maps a writable layer to the scope_user value that stores
// it. It refuses the folder layer rather than defaulting, because
// silently writing a claim to the wrong layer is the one mistake this
// type exists to prevent.
func (s SeriesSource) ScopeUser(userID string) (string, error) {
	switch s {
	case SeriesSourceShared:
		return SharedSeriesScope, nil
	case SeriesSourcePersonal:
		if userID == "" {
			return "", fmt.Errorf("%w: a personal series claim needs a user",
				ErrInvalidInput)
		}
		return userID, nil
	default:
		return "", fmt.Errorf("%w: series claim scope %q", ErrInvalidInput, s)
	}
}

// BookSeries is one series membership. Position is absent when the
// source named a series without a place in it; a missing position is
// never invented.
type BookSeries struct {
	SeriesID       string
	Name           string
	NormalizedName string
	Position       *float64
	// Source is the layer this membership was resolved from.
	Source SeriesSource
}

// ContributorRoleAuthor is the role a person holds when they wrote the
// book. Roles are normalized on the way in (MARC's "aut" and the word
// "author" are the same role), so a reader of the catalog compares
// against this rather than against whatever the file said.
const ContributorRoleAuthor = "author"

// BookContributor is one contributor in one role. A person credited
// twice in different roles is two rows over one contributor entity.
type BookContributor struct {
	ContributorID  string
	Name           string
	NormalizedName string
	Role           string
	Position       int
}

// CatalogBookRelations holds the relationship rows a catalog payload is
// drawn from, for a whole page of books at once, keyed by book id. It
// answers "what does a shelf need to show these books".
type CatalogBookRelations struct {
	Contributors         map[string][]BookContributor
	Series               map[string][]BookSeries
	SeriesSource         map[string]SeriesSource
	SeriesClaimUpdatedAt map[string]*time.Time
}

// CatalogSeriesVolume is one active book in the primary series the
// catalog presents for it. A book may claim several series, but Liseur's
// shelf files it under the first effective membership, ordered by the
// resolved normalized name and then id. The web shelf uses the same rule
// so the two clients never group one book differently.
type CatalogSeriesVolume struct {
	SeriesID   string
	SeriesName string
	BookID     string
	Title      string
	MediaType  string
	Position   *float64
	CreatedAt  time.Time
}

// SeriesClaimItem is one membership in a claim. Exactly one of SeriesID
// and Name identifies the series: an id points at one that exists, a
// name creates it if it does not. Position is absent when the claimant
// says a book belongs to a series without saying where in it.
type SeriesClaimItem struct {
	SeriesID string
	Name     string
	Position *float64
}

// SeriesPlacement is where one book sits in one series, for a bulk
// renumbering.
type SeriesPlacement struct {
	BookID   string
	Position *float64
}

// BookSeriesLayers is one book's series seen at every layer at once
// (ADR-0018). Folder is what the last pass observed. Shared and Personal
// are nil when that layer holds no claim, which is what distinguishes
// "nobody said" from the empty claim "in no series". Effective is the
// one in force, and is what every other read returns.
type BookSeriesLayers struct {
	Folder            []BookSeries
	Shared            []BookSeries
	Personal          []BookSeries
	Effective         []BookSeries
	SharedUpdatedAt   *time.Time
	PersonalUpdatedAt *time.Time
	// Source names the layer Effective came from.
	Source SeriesSource
}

// SeriesClaimOutcome says whether a timestamped claim mutation changed the
// current layer. A duplicate is the retry of an already deleted revision;
// a stale mutation does not supersede the current revision and changes
// nothing.
type SeriesClaimOutcome string

const (
	SeriesClaimApplied   SeriesClaimOutcome = "applied"
	SeriesClaimStale     SeriesClaimOutcome = "stale"
	SeriesClaimDuplicate SeriesClaimOutcome = "duplicate"
)

// SeriesClaimMutation is what a claim write carries besides the claim
// itself: when it happened, which revision the writer had seen, and the
// key that makes a retry recognisable as one.
//
// A struct rather than three trailing parameters, so that a caller with
// nothing to say about idempotency writes SeriesClaimMutation{At: now}
// and still gets told by the compiler when the contract changes.
type SeriesClaimMutation struct {
	// ClientTS is the writer's own name for this mutation. Replaying it
	// with the same claim is a duplicate, not a second write; reusing it
	// for a different claim is a conflict.
	ClientTS string
	// IfUpdatedAt is the revision the writer had seen. A mutation made
	// against an older revision than the one stored is stale and changes
	// nothing. Nil means the writer had seen no revision at all.
	IfUpdatedAt *time.Time
	// At is the server's clock reading for this write.
	At time.Time
}

// ClaimRevision rounds a revision to the precision the protocol
// promises. A revision is quoted back by a client as a precondition, and
// a client that keeps it as milliseconds since the epoch — which an
// Android reader storing it in a database column does — cannot quote a
// microsecond back. Left finer, every precondition would miss and every
// writer would be told it was stale forever.
func ClaimRevision(at time.Time) time.Time {
	return at.UTC().Truncate(time.Millisecond)
}

// SameClaimRevision says whether a quoted revision names the stored one.
// It compares at the protocol's precision rather than exactly, so a row
// written before revisions were rounded still answers to the
// millisecond a client can hold.
func SameClaimRevision(stored, quoted time.Time) bool {
	return ClaimRevision(stored).Equal(ClaimRevision(quoted))
}

// SeriesClaimRequestHash identifies the stable state stated by one
// idempotent claim request. A client_ts may only be reused for this same
// state; the server rejects a reuse that would mean something different.
func SeriesClaimRequestHash(deleted bool, items []SeriesClaimItem) string {
	if items == nil {
		items = []SeriesClaimItem{}
	}
	raw, _ := json.Marshal(struct {
		Deleted bool              `json:"deleted"`
		Items   []SeriesClaimItem `json:"items"`
	}{deleted, items})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// UserBookWork is the privacy boundary between a shared catalog book and
// a reader's own history. The catalog is deliberately shared; a reading
// position never is.
type UserBookWork struct {
	UserID    string
	FolderID  string
	BookID    string
	WorkID    string
	CreatedAt time.Time
}

// CatalogBookCursor is the exclusive cursor for catalog listings. It
// pairs the sort key with the id so that books created in the same
// instant still page deterministically.
type CatalogBookCursor struct {
	CreatedAt time.Time
	ID        string
	// SeriesPosition is the leading sort key for a series listing, whose
	// order is the series' own rather than the order books were
	// scanned. It is nil for every other listing, and for a book with no
	// place in the series — which sorts last, because an unplaced book
	// is an unanswered question rather than book zero.
	//
	// It is in the cursor because it is in the ORDER BY: one
	// reconciliation pass stamps a whole folder with the same
	// created_at, so a cursor that named only (created_at, id) would
	// filter on a key the rows are not ordered by and silently skip
	// volumes on the second page.
	SeriesPosition *float64
}

// EntityKind names one of the three library-wide metadata entity tables.
// It is a closed set because it selects a table: a caller cannot ask for
// a kind the schema does not have, and the store never interpolates a
// caller's string into SQL.
type EntityKind string

const (
	EntitySeries      EntityKind = "series"
	EntityContributor EntityKind = "contributor"
	EntityTag         EntityKind = "tag"
)

// Valid reports whether the kind names a table the store knows.
func (k EntityKind) Valid() bool {
	return k == EntitySeries || k == EntityContributor || k == EntityTag
}

// CatalogEntity is one library-wide series, contributor or tag, together
// with how many of the library's active books claim it.
//
// Name is what the asking reader sees. For a series that can be a
// rename (ADR-0020), in which case ScannedName still holds what the last
// pass observed and NameSource names the layer the display name came
// from — the two things a client needs to offer a revert. For every
// other kind the two names are equal and the source is the folder.
type CatalogEntity struct {
	ID             string
	Kind           EntityKind
	Name           string
	NormalizedName string
	ScannedName    string
	NameSource     SeriesSource
	BookCount      int
	CreatedAt      time.Time
}

// MaxSeriesNameBytes bounds a series name, whether a pass observed it or
// a reader typed it: a renamed series and a scanned one are the same
// kind of thing.
const MaxSeriesNameBytes = 512

// SeriesBinding is one answer to "what does this observed name mean"
// (ADR-0021). A merge writes one with no folder, so the absorbed name
// resolves to the survivor everywhere; a folder-wise split writes one
// scoped to the folder whose books left.
//
// FolderID is empty when the binding applies everywhere. FolderName is
// filled in for display and is empty for a global binding; it is a
// folder's name, never its path, which never reaches a listing.
type SeriesBinding struct {
	ID             string
	FolderID       string
	FolderName     string
	Name           string
	NormalizedName string
	SeriesID       string
	CreatedAt      time.Time
	CreatedBy      string
}

// SeriesFolderCount is one folder's contribution to a shelf: the unit a
// folder-wise split moves.
type SeriesFolderCount struct {
	FolderID  string
	Name      string
	BookCount int
}

// MaxEntityListLimit bounds one entity listing. Entity lists are browsed
// by a person, and a folder with more distinct tags than this has a
// problem no page size can fix.
const MaxEntityListLimit = 500

// SearchQuery asks one folder for the books matching some words.
//
// It carries no reading-state filter, and that is a rule rather than an
// omission (ADR-0004): a catalog-only credential must not be able to
// observe reading state, and the surest way to keep that true is for the
// catalog's search to have no vocabulary for it.
type SearchQuery struct {
	FolderID string
	// UserID is who is asking. Series filters and series facets resolve
	// through that reader's override layers (ADR-0018); the full-text
	// index is deliberately shared and keeps indexing what the folder
	// observed.
	UserID string
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
// which is a question with a short answer; browsing a whole folder is
// what the paged listings are for.
const MaxSearchLimit = 100

// MaxSearchFacets bounds how many values of each kind a result
// describes. A facet list is a set of suggestions, and a suggestion
// nobody will read is only a bigger response.
const MaxSearchFacets = 20

// SearchResult is one folder's answer: the matching books, best first,
// and what those books have in common.
type SearchResult struct {
	Books  []CatalogBook
	Facets []SearchFacet
	// Truncated says the answer was cut at the limit, so a caller can
	// say "narrow this" rather than implying it found everything.
	Truncated bool
}

// SearchFacet is one entity the matching books share, with how many of
// them claim it. Counts are over the matched set rather than the folder,
// because a facet's job is to describe the answer.
type SearchFacet struct {
	Kind      EntityKind
	ID        string
	Name      string
	BookCount int
}

// ---------------------------------------------------------------------
// Reconciliation
// ---------------------------------------------------------------------

// ObservedBook is one publication a scan saw, described completely
// enough to write a catalog row from it. A pass builds these and hands
// the whole set to ReconcileFolder; there is no intermediate job, no
// staging state and nothing persisted between the two.
type ObservedBook struct {
	// RelativePath and CalibreID are the two identity keys. Exactly one
	// of them is used, decided by the folder's kind — path for a plain
	// folder, Calibre id for a Calibre one.
	RelativePath string
	CalibreID    *int64

	SizeBytes         int64
	MTime             time.Time
	ContentSHA256     string
	OriginalFilename  string
	MediaType         string
	CoverRelativePath *string
	CoverSHA256       string

	Title         string
	Subtitle      string
	Description   string
	Publisher     string
	PublishedDate string

	Identifiers  []BookIdentifier
	Languages    []string
	Tags         []string
	Series       []ObservedSeries
	Contributors []ObservedContributor

	// Replaces says the bytes at this identity changed, so the existing
	// row is not this book. Rule 4 of ADR-0017: content change is not
	// identity transfer. The old row is deleted — taking its
	// identifiers, its relations and its user_book_works mapping with it
	// — and a new one inserted, in the same transaction, so no two rows
	// ever claim the same path.
	//
	// It is never set for a Calibre folder: there the curator's database
	// is the identity, not the bytes, so a changed file is an update.
	Replaces bool

	// Unchanged says the pass recognised this file by its stat and did
	// not re-read it, so the metadata fields above are empty rather than
	// blank. The store refreshes only the stat and the status; it must
	// not overwrite a book's title with the nothing that is here.
	//
	// This is why an unchanged file still produces an observation: being
	// seen is what keeps a book out of the missing list, and a pass that
	// skipped its unchanged files entirely would mark the whole folder
	// gone on its second run.
	Unchanged bool

	// Unservable says the folder's own catalog still holds this book but
	// this server has no file it can serve for it — a Calibre book whose
	// only remaining format is one this server does not read.
	//
	// It exists to keep that case apart from deletion. A Calibre folder
	// purges what its metadata.db no longer lists (ADR-0022), and a book
	// converted to another format is still listed: it is marked missing,
	// its row and every reader's mapping to it are kept, and the file
	// coming back restores it. Only the identity key is read from such an
	// observation; the metadata fields are empty and nothing is written
	// from them.
	Unservable bool
}

// ObservedSeries is a series a scan attributed to a book, by display
// name. The store resolves it to a library-wide entity, so the same
// series observed in two folders is one row (ADR-0019).
type ObservedSeries struct {
	Name     string
	Position *float64
}

// ObservedContributor is a person a scan attributed to a book, by
// display name and normalized role.
type ObservedContributor struct {
	Name     string
	Role     string
	Position int
}

// ReconcileResult counts what one pass changed, for the log. It is not
// persisted: a pass holds no state, and running it twice is running it
// once.
type ReconcileResult struct {
	Added    int
	Updated  int
	Replaced int
	Missing  int
	Returned int
	// Purged counts books deleted because the folder's own catalog no
	// longer lists them (ADR-0022). It is only ever non-zero for a
	// Calibre folder: elsewhere absence is evidence about a disk, and a
	// book that vanished is marked missing and kept.
	Purged int
	// Rekeyed counts readers whose work graph followed a book whose bytes
	// changed. A Calibre metadata edit rewrites the publication, so the
	// digest a reader's device will report next is not the one their work
	// was resolved from; registering the new one keeps the position with
	// the book instead of minting a second work for it.
	Rekeyed int
}

// Changed reports whether the pass wrote anything, so a caller can log a
// no-op quietly.
func (r ReconcileResult) Changed() bool {
	return r.Added+r.Updated+r.Replaced+r.Missing+
		r.Returned+r.Purged+r.Rekeyed > 0
}

// KnownBook is what ReconcileFolder's caller already has on record for
// one book, which is all a diff needs: enough to decide unchanged,
// changed or gone without reading a file.
type KnownBook struct {
	ID            string
	Status        BookStatus
	RelativePath  string
	SizeBytes     int64
	MTime         time.Time
	ContentSHA256 string
	CalibreID     *int64
	CoverSHA256   string
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
	// ListUsersPage is the admin panel's user list: cursor-paginated by
	// name, because the list grows with the instance. Ask for one row
	// more than you display and the extra row is the answer to "is
	// there another page", with no second counting query.
	ListUsersPage(ctx context.Context, afterName string, limit int) ([]User, error)
	// SetUserAdmin moves the admin flag. It is the only way the flag
	// moves, and it does three things in one locked transaction so that
	// nothing can interleave: it applies the last-enabled-admin guard
	// (ErrLastAdmin), it writes the flag, and on demotion it revokes
	// every unrevoked admin-scoped token the account holds — ScopeAdmin
	// implies every other scope, so a token outliving the role would
	// keep full API authority.
	SetUserAdmin(ctx context.Context, userID string, admin bool) error
	// SetUserDisabled stops an account, or starts it again. Disabling
	// applies the last-enabled-admin guard, sets disabled_at and
	// revokes every web and login session, in one transaction — there
	// is no moment where the account is disabled but a session
	// survives, and a failure is a failure rather than a half-done
	// disable.
	//
	// Enabling clears the flag and nothing else. API tokens, kosync
	// slots and koplugin capabilities resume working, because they were
	// never revoked; sessions do not, because they were.
	//
	// The refusal that matters is not here: the credential lookups
	// themselves (AuthSessionByHash, TokenByHashGlobal,
	// KosyncDeviceByKey, RedeemPairingCode, KopluginDeviceByToken) all
	// join against users and behave as if the credential did not exist
	// while its owner is disabled, so no handler can forget a check it
	// does not make.
	SetUserDisabled(ctx context.Context, userID string, disabled bool, at time.Time) error
	// CreateFirstAdmin creates an account with the admin flag already
	// set, but only while the instance holds no users at all;
	// otherwise it returns ErrConflict. It exists for the web UI's
	// first-run setup, which is the one place an unauthenticated caller
	// may make an administrator, and the emptiness check has to be
	// inside the same locked transaction as the insert or two people
	// opening the page at once both become one.
	CreateFirstAdmin(ctx context.Context, u User) error
	// AdminCounts is the one aggregate read the admin panel makes. It
	// crosses users deliberately and is listed here beside ListUsers
	// for the same reason: it is instance administration, not access to
	// anybody's data. It returns integers and timestamps only — never a
	// title, a path, an error string or another user's id — which is
	// what lets the overview and maintenance pages exist without
	// weakening a single tenant-isolation guarantee.
	AdminCounts(ctx context.Context) (AdminCounts, error)

	// Tokens.
	CreateToken(ctx context.Context, t Token) error
	TokenByHash(ctx context.Context, userID, sha256 string) (Token, error)
	ListTokens(ctx context.Context, userID string) ([]Token, error)
	UpdateTokenScopes(ctx context.Context, userID, tokenID string, scopes ScopeSet) error
	RevokeToken(ctx context.Context, userID, tokenID string) error
	// DeleteToken removes a token outright, for credentials the server
	// issued to itself and nobody ever asked to see. A revoked row is a
	// record that someone's device was cut off and is worth keeping; an
	// expired browser-reader token is neither, and keeping it would let
	// the tokens table grow by one row an hour for as long as a person
	// reads. Returns ErrNotFound if the token is not this user's.
	DeleteToken(ctx context.Context, userID, tokenID string) error
	TouchToken(ctx context.Context, userID, tokenID string, at time.Time) error

	// -----------------------------------------------------------------
	// Folders.
	//
	// A folder has no owner and no access list, so none of these take a
	// user id: every signed-in account sees every folder's books. Only
	// an administrator is shown RootPath, and that is enforced at the
	// handler edge, not here.
	// -----------------------------------------------------------------

	CreateFolder(ctx context.Context, folder Folder) error
	FolderByID(ctx context.Context, folderID string) (Folder, error)
	// SetFolderUploads turns a folder's upload permission on or off
	// (ADR-0023). Returns ErrNotFound if the folder is gone.
	SetFolderUploads(ctx context.Context, folderID string, accepts bool, at time.Time) error
	// ListFolders pages folders ordered by name then id. after is a
	// FolderCursor, empty for the first page.
	ListFolders(ctx context.Context, after string, limit int) ([]Folder, error)
	// DeleteFolder removes a folder and, by cascade, every catalog row
	// that hung off it. Nothing beneath its root path is touched: the
	// files were never this server's to delete.
	DeleteFolder(ctx context.Context, folderID string) error
	// DeleteMissingBook removes one catalog book a pass has already
	// marked missing, with the same cascade and the same entity
	// collection a pass that dropped it would run. It is how an
	// administrator retires a file that is not coming back.
	//
	// A book that is still active is ErrInvalidInput: an active book is
	// re-added by the next pass, so deleting one would be theatre. A
	// book that does not exist is ErrNotFound. Readers' works are not
	// deleted with it — a work with reading history survives its book
	// and becomes an entry only its own reader can remove.
	DeleteMissingBook(ctx context.Context, bookID string) error
	// DeleteCatalogBook removes one catalog book whose file this server
	// has just deleted (ADR-0025), with the same cascade and the same
	// entity collection DeleteMissingBook runs.
	//
	// It is the counterpart of an upload, and bounded the same way: a
	// book whose folder does not accept uploads is ErrInvalidInput, and
	// that is re-read inside the transaction rather than trusted from a
	// check the caller made earlier. Unlike DeleteMissingBook it does
	// not require the book to be marked missing — the file is already
	// gone by the time this is called, so no pass will put it back.
	//
	// ForgetReadingFor names the one reader whose work goes with it, and
	// only ever theirs.
	DeleteCatalogBook(
		ctx context.Context, bookID string, opts DeleteBookOptions,
	) (DeleteBookResult, error)

	// -----------------------------------------------------------------
	// Reconciliation.
	// -----------------------------------------------------------------

	// BooksInFolder returns what the catalog already records for one
	// folder — enough to decide unchanged, changed or gone without
	// reading a file. It includes missing books, because a missing book
	// that reappears must be recognised rather than added again.
	BooksInFolder(ctx context.Context, folderID string) ([]KnownBook, error)
	// ReconcileFolder writes one pass's findings in a single
	// transaction: observed books are inserted or updated with their
	// relations, and books the pass did not observe are marked missing.
	//
	// complete is the caller's honest answer to "did I see the whole
	// folder". It is a parameter rather than a comment because two of
	// ADR-0017's four rules live here and a caller must not be able to
	// forget them:
	//
	//   - An incomplete pass never marks anything missing. Any per-file
	//     read or parse failure, and any hit against a scan bound, makes
	//     a pass incomplete. It may still record what it saw.
	//   - A pass that observed nothing at all never marks anything
	//     missing either, whatever it claims about completeness. An
	//     unmounted mount point is usually still readable and empty,
	//     which is indistinguishable from a folder somebody emptied —
	//     and hiding a whole catalog is the worse of the two errors.
	//
	// An observation whose Replaces is set deletes the existing row and
	// its cascade before inserting, so identity is never transferred to
	// bytes that merely arrived at the same path.
	//
	// In a Calibre folder the two guards above gate a deletion rather
	// than a flag (ADR-0022): metadata.db is a catalog somebody curates,
	// so a book a complete pass no longer finds there was removed, and
	// the row goes with the reader mappings that hung off it. A book
	// metadata.db still lists but no longer offers a servable file is
	// observed as Unservable and is marked missing, not purged.
	ReconcileFolder(ctx context.Context, folderID string, observed []ObservedBook, complete bool, at time.Time) (ReconcileResult, error)

	// -----------------------------------------------------------------
	// Catalog reads.
	// -----------------------------------------------------------------

	CatalogBookByID(ctx context.Context, bookID string) (CatalogBook, error)
	// CatalogBookByDigest finds an active book by its content digest.
	//
	// It is what makes an upload idempotent (ADR-0023): the bytes are
	// the key, so a client that retries a transfer costs one indexed
	// lookup and creates nothing. It is also how an upload finds the
	// book its own reconcile pass just made, which is why it does not
	// take a folder id — the same file may already be somewhere else,
	// and that copy is the answer.
	CatalogBookByDigest(ctx context.Context, sha string) (CatalogBook, error)
	// ListCatalogBooks pages one folder's books, oldest first.
	ListCatalogBooks(ctx context.Context, folderID string, after *CatalogBookCursor, limit int) ([]CatalogBook, error)
	// ListRecentCatalogBooks pages the same books newest first, for the
	// "recently added" shelf and the OPDS feed of the same name.
	ListRecentCatalogBooks(ctx context.Context, folderID string, before *CatalogBookCursor, limit int) ([]CatalogBook, error)
	// CatalogBookRelationsForBooks reads the contributors and series of a
	// whole page of books at once, so that rendering a shelf costs one
	// query rather than one per book (ADR-0015).
	//
	// userID is who is asking, because series memberships resolve
	// through that reader's override layers (ADR-0018). Contributors and
	// every other relation stay shared.
	CatalogBookRelationsForBooks(ctx context.Context, userID string, bookIDs []string) (CatalogBookRelations, error)
	// CatalogSeriesVolumesForBooks returns every active primary-series
	// volume in folderID for the series named by bookIDs. It is the
	// batched expansion a mixed shelf needs: one book on the current page
	// can stand for its whole folder-local pile without an entity lookup
	// per card. Series membership and display names resolve for userID.
	CatalogSeriesVolumesForBooks(ctx context.Context, userID, folderID string, bookIDs []string) ([]CatalogSeriesVolume, error)
	// ListCatalogEntities pages the library's entities of one kind by
	// normalized name, with the number of books claiming each. Series
	// counts are resolved for userID; the other kinds ignore it.
	ListCatalogEntities(ctx context.Context, userID string, kind EntityKind, after string, limit int) ([]CatalogEntity, error)
	// CatalogEntityByID reads one entity, so a page can name what it is
	// listing without scanning for it.
	CatalogEntityByID(ctx context.Context, userID, entityID string, kind EntityKind) (CatalogEntity, error)
	// ListBooksByEntity pages the books claiming one entity, oldest
	// first, across every folder they were found in (ADR-0019).
	//
	// It returns the cursor for the next page itself, because the sort
	// key depends on the kind and only the store knows it. The cursor is
	// nil when the page is the last one.
	ListBooksByEntity(ctx context.Context, userID, entityID string, kind EntityKind, after *CatalogBookCursor, limit int) ([]CatalogBook, *CatalogBookCursor, error)
	// SearchCatalogBooks answers one folder's search, best first, with
	// facets describing the answer.
	SearchCatalogBooks(ctx context.Context, query SearchQuery) (SearchResult, error)
	// AvailableBookMediaTypes reports the distinct media types a folder's
	// books carry, so a feed can advertise what it actually holds.
	AvailableBookMediaTypes(ctx context.Context, folderID string) ([]string, error)
	// CatalogBookIdentifiers reads one book's publication identifiers,
	// which are the evidence a work resolution runs on.
	CatalogBookIdentifiers(ctx context.Context, bookID string) ([]BookIdentifier, error)
	// CatalogAuthorsForBooks maps book id to author display names, for
	// the identity backfill that links a catalog book to a sync work.
	CatalogAuthorsForBooks(ctx context.Context, bookIDs []string) (map[string][]string, error)

	// -----------------------------------------------------------------
	// Series claims (ADR-0018).
	//
	// A claim is one layer's whole answer to "which series is this book
	// in", overriding what the last pass observed. The shared layer is
	// written by administrators and seen by everyone without a personal
	// claim; a personal claim is seen by its author alone. Nothing here
	// touches book_series, which keeps meaning what the folder said.
	// -----------------------------------------------------------------

	// SetBookSeriesOverride replaces one layer's claim about one book.
	// An empty items slice is the claim "this book is in no series",
	// which is different from having no claim at all. Items naming a
	// series that does not exist create it, folding by normalized name
	// exactly as a pass would.
	//
	// userID is the writer; it is also the layer for a personal claim.
	SetBookSeriesOverride(
		ctx context.Context, userID, bookID string, scope SeriesSource,
		items []SeriesClaimItem, mutation SeriesClaimMutation,
	) (SeriesClaimOutcome, error)
	// ClearBookSeriesOverride drops one layer's claim, so the book falls
	// back to the layer beneath. Clearing a claim that is not there is
	// not an error: the caller asked for an absence and got one.
	ClearBookSeriesOverride(
		ctx context.Context, userID, bookID string, scope SeriesSource,
		mutation SeriesClaimMutation,
	) (SeriesClaimOutcome, error)
	// ReorderSeries restates the positions of books within one series in
	// one layer, in one transaction.
	//
	// It writes a claim per book, preserving each book's other series
	// memberships as they resolve for userID: renumbering a trilogy must
	// not silently drop a volume's membership of an omnibus.
	ReorderSeries(ctx context.Context, userID, seriesID string, scope SeriesSource, order []SeriesPlacement, at time.Time) error
	// BookSeriesLayers reads all three layers for one book, so an editor
	// can show what the folder said, what was claimed, and which of them
	// is in force.
	BookSeriesLayers(ctx context.Context, userID, bookID string) (BookSeriesLayers, error)

	// -----------------------------------------------------------------
	// Series names (ADR-0020).
	//
	// A rename is a display layer, in the same two scopes a claim uses.
	// It never touches series.normalized_name, which stays what a scan
	// observed and stays the only thing a pass resolves against: a
	// rename that moved the fold key would be undone by the next pass.
	// -----------------------------------------------------------------

	// SetSeriesName renames one series in one layer. It returns
	// ErrConflict when the name already belongs to another series in
	// that layer's view — which is a request to merge two shelves, and
	// merging is not a thing this store can do.
	SetSeriesName(ctx context.Context, userID, seriesID string, scope SeriesSource, name string, at time.Time) error
	// ClearSeriesName drops one layer's rename, so the series falls back
	// to the layer beneath and ultimately to what the scan said.
	// Clearing a name that is not there is not an error.
	ClearSeriesName(ctx context.Context, userID, seriesID string, scope SeriesSource) error

	// -----------------------------------------------------------------
	// Merging and splitting a series (ADR-0021).
	//
	// A merge is not a delete and a split is not an insert. The fold key
	// belongs to the disk: resolveEntityTx turns an observed name into a
	// series, so a shelf rearranged only in the database is rearranged
	// back by the next pass that observes the old name. Both operations
	// therefore write a binding — what an observed name means, here or
	// everywhere — and the resolver reads it first.
	//
	// Both are shared and admin-written. A merge states the library's
	// shape, not one reader's view of it, and it is the only version of
	// the operation a future Calibre write-back could ever act on.
	// -----------------------------------------------------------------

	// MergeSeries folds one series into another, in one transaction:
	// memberships and claims are repointed, a global binding keeps the
	// absorbed name resolving to the survivor, and the absorbed row is
	// deleted. Positions are left alone — inventing an order across two
	// shelves is a guess, and ReorderSeries already exists.
	//
	// Merging a series into itself is ErrInvalidInput; either series
	// missing is ErrNotFound, which is also how merging into a series
	// that has itself been absorbed is refused.
	MergeSeries(ctx context.Context, userID, seriesID, intoID string, at time.Time) (string, error)
	// SplitSeriesFolder takes one folder's books off a shelf onto a new
	// series of its own and binds that folder's observed names to it, so
	// the next pass over the folder agrees. It returns the new series id.
	//
	// Splitting a shelf every one of whose books came from that folder
	// is ErrInvalidInput: that is a rename. Splitting off a folder with
	// no books on the shelf is ErrNotFound. A name another series
	// already holds is ErrConflict.
	SplitSeriesFolder(ctx context.Context, userID, seriesID, folderID, name string, at time.Time) (string, error)
	// SeriesBindings lists the names that fold into one series, so a
	// shelf can show what it absorbed and offer to undo it.
	SeriesBindings(ctx context.Context, seriesID string) ([]SeriesBinding, error)
	// DeleteSeriesBinding removes one binding. The next pass observing
	// that name resolves it afresh, which is how a merge is undone and
	// how a split is put back.
	DeleteSeriesBinding(ctx context.Context, bindingID string) error
	// SeriesFolders names the folders whose books are on one shelf, most
	// books first, so a page can offer a split only where there is
	// something to split and can say how much would move.
	SeriesFolders(ctx context.Context, seriesID string) ([]SeriesFolderCount, error)

	// -----------------------------------------------------------------
	// The bridge to per-user reading state.
	// -----------------------------------------------------------------

	// ResolveCatalogBookWork joins one catalog book to one of the
	// caller's own works and records the mapping, in one transaction.
	// The catalog is shared; this mapping never is. A low-confidence or
	// conflicting resolution links nothing.
	ResolveCatalogBookWork(ctx context.Context, userID, bookID string, proposed Work, editions []Edition, ids []Identifier, confirmed bool, at time.Time) (WorkResolution, error)
	// UserBookWork reads the caller's own link for one book.
	UserBookWork(ctx context.Context, userID, bookID string) (UserBookWork, error)
	// WorkBookIDs lists the caller's books mapped to one work.
	WorkBookIDs(ctx context.Context, userID, workID string) ([]string, error)

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
	// DeleteWork removes one of the caller's works and everything that
	// hangs off it — ops, sessions, rollups, editions, aliases and the
	// book mapping — in one transaction. It is the reader's own
	// decision to forget a book, and the deliberate exception to the
	// append-only rule (ADR-0024); it is not a way to edit history,
	// because the unit is the whole work or nothing.
	//
	// Only a work no book on this server backs can be deleted:
	// ErrInvalidInput when a mapping still names a catalog book, so
	// that a file this server currently holds — or holds and cannot
	// find today — never loses its reading state by a click. It returns
	// ErrNotFound when the work is not this user's.
	DeleteWork(ctx context.Context, userID, workID string) error

	// User settings.
	UpdateUserSettings(ctx context.Context, userID, timezone string, kosyncEnabled, kopluginEnabled bool) error
	// SetUserPassword writes the argon2id hash and revokes the
	// account's auth sessions — web and login both — in one
	// transaction, so there is no moment where the password has changed
	// and a session opened with the old one still works. keepSessionID
	// spares one session: the self-service form passes the caller's own
	// so that changing your password does not sign you out of the tab
	// you did it in, and an administrator's reset passes "" so that
	// nothing survives.
	SetUserPassword(ctx context.Context, userID, argon2Hash, keepSessionID string) error
	// RevokeAllUserCredentials revokes, in one transaction, every way
	// the account can authenticate: API tokens, kosync device slots,
	// koplugin capabilities, auth sessions, and unredeemed pairing
	// codes. The pairing codes are the reason this is one method rather
	// than five calls — a code left behind mints a fresh device slot
	// minutes after the operator was told everything was gone.
	RevokeAllUserCredentials(ctx context.Context, userID string, at time.Time) error

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

// NewID mints an opaque row identifier. Reconciliation is the one place
// the store invents ids rather than being handed them: a pass discovers
// books, so nothing upstream of it knows which are new.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on any platform this runs on, and a
		// catalog id that is not unique is a corrupted catalog. There is
		// no sensible partial answer.
		panic("store: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
