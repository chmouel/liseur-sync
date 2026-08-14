package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func scanLibrary(row interface{ Scan(...any) error }) (store.AccessibleLibrary, error) {
	var out store.AccessibleLibrary
	var root, refreshError sql.NullString
	var lastRefresh, lastAttempt, requested sql.NullString
	var created, updated string
	var refreshSeconds int64
	err := row.Scan(
		&out.Library.ID,
		&out.Library.OwnerUserID,
		&out.Library.QuotaUserID,
		&out.Library.Source,
		&out.Library.Storage,
		&out.Library.Refresh,
		&refreshSeconds,
		&out.Library.Name,
		&root,
		&out.Library.ConfigJSON,
		&created,
		&updated,
		&lastRefresh,
		&lastAttempt,
		&refreshError,
		&requested,
		&out.Role,
	)
	if err != nil {
		return out, err
	}
	if root.Valid {
		out.Library.RootPath = &root.String
	}
	if refreshError.Valid {
		out.Library.LastRefreshError = &refreshError.String
	}
	out.Library.RefreshInterval = store.RefreshIntervalFrom(refreshSeconds)
	if out.Library.CreatedAt, err = parseTime(created); err != nil {
		return out, err
	}
	if out.Library.UpdatedAt, err = parseTime(updated); err != nil {
		return out, err
	}
	if out.Library.LastRefreshAt, err = parseTimePtr(lastRefresh); err != nil {
		return out, err
	}
	if out.Library.LastRefreshAttemptAt, err = parseTimePtr(lastAttempt); err != nil {
		return out, err
	}
	out.Library.RefreshRequestedAt, err = parseTimePtr(requested)
	return out, err
}

func scanCatalogBook(row interface{ Scan(...any) error }) (store.CatalogBook, error) {
	var book store.CatalogBook
	var titleLocked, subtitleLocked, descriptionLocked int
	var publisherLocked, publishedDateLocked int
	var identifiersLocked, languagesLocked, tagsLocked int
	var genresLocked, seriesLocked, contributorsLocked int
	var created, updated string
	var trashed, trashExpires, reviewReason sql.NullString
	err := row.Scan(
		&book.ID,
		&book.LibraryID,
		&book.Status,
		&book.Title,
		&book.TitleSource,
		&titleLocked,
		&book.Subtitle,
		&book.SubtitleSource,
		&subtitleLocked,
		&book.Description,
		&book.DescriptionSource,
		&descriptionLocked,
		&book.Publisher,
		&book.PublisherSource,
		&publisherLocked,
		&book.PublishedDate,
		&book.PublishedDateSource,
		&publishedDateLocked,
		&book.RawMetadataJSON,
		&created,
		&updated,
		&trashed,
		&trashExpires,
		&book.Revision,
		&identifiersLocked,
		&languagesLocked,
		&tagsLocked,
		&genresLocked,
		&seriesLocked,
		&contributorsLocked,
		&reviewReason,
	)
	if err != nil {
		return book, err
	}
	book.ReviewReason = reviewReason.String
	book.TitleLocked = titleLocked != 0
	book.SubtitleLocked = subtitleLocked != 0
	book.DescriptionLocked = descriptionLocked != 0
	book.PublisherLocked = publisherLocked != 0
	book.PublishedDateLocked = publishedDateLocked != 0
	book.SetLocks = store.MetadataSetLocks{
		Identifiers:  identifiersLocked != 0,
		Languages:    languagesLocked != 0,
		Tags:         tagsLocked != 0,
		Genres:       genresLocked != 0,
		Series:       seriesLocked != 0,
		Contributors: contributorsLocked != 0,
	}
	if book.CreatedAt, err = parseTime(created); err != nil {
		return book, err
	}
	if book.UpdatedAt, err = parseTime(updated); err != nil {
		return book, err
	}
	if trashed.Valid {
		tm, err := parseTime(trashed.String)
		if err != nil {
			return book, err
		}
		book.TrashedAt = &tm
	}
	if trashExpires.Valid {
		tm, err := parseTime(trashExpires.String)
		if err != nil {
			return book, err
		}
		book.TrashExpiresAt = &tm
	}
	return book, nil
}

const libraryColumns = `l.id, l.owner_user_id, l.quota_user_id,
	l.source, l.storage, l.refresh, l.refresh_interval_seconds, l.name,
	l.root_path, l.config_json, l.created_at, l.updated_at,
	l.last_refresh_at, l.last_refresh_attempt_at, l.last_refresh_error,
	l.refresh_requested_at,
	CASE WHEN l.owner_user_id = ? THEN 'manage' ELSE a.role END`

const bookColumns = `b.id, b.library_id, b.status,
	b.title, b.title_source, b.title_locked,
	b.subtitle, b.subtitle_source, b.subtitle_locked,
	b.description, b.description_source, b.description_locked,
	b.publisher, b.publisher_source, b.publisher_locked,
	b.published_date, b.published_date_source, b.published_date_locked,
	b.raw_metadata_json, b.created_at, b.updated_at, b.trashed_at, b.trash_expires_at,
	b.revision, b.identifiers_locked, b.languages_locked, b.tags_locked,
	b.genres_locked, b.series_locked, b.contributors_locked, b.review_reason`

func checkLibraryRole(role store.LibraryRole) error {
	if !role.Valid() {
		return fmt.Errorf("invalid library role %q", role)
	}
	return nil
}

func (s *Store) CreateLibrary(ctx context.Context, library store.Library) error {
	if library.UpdatedAt.IsZero() {
		library.UpdatedAt = library.CreatedAt
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO libraries
		 (id, owner_user_id, quota_user_id, source, storage, refresh,
		  refresh_interval_seconds, name, root_path, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		library.ID, library.OwnerUserID, library.QuotaUserID,
		string(library.Source), string(library.Storage), string(library.Refresh),
		store.RefreshSeconds(library.RefreshInterval),
		library.Name, library.RootPath, library.ConfigJSON,
		formatTime(library.CreatedAt), formatTime(library.UpdatedAt))
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) LibraryByID(ctx context.Context, userID, libraryID string, required store.LibraryRole) (store.AccessibleLibrary, error) {
	if err := checkLibraryRole(required); err != nil {
		return store.AccessibleLibrary{}, err
	}
	out, err := scanLibrary(s.db.QueryRowContext(ctx,
		`SELECT `+libraryColumns+`
		 FROM libraries l
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage' OR (? = 'read' AND a.role = 'read'))`,
		userID, userID, libraryID, userID, string(required)))
	if errors.Is(err, sql.ErrNoRows) {
		return out, store.ErrNotFound
	}
	return out, err
}

func (s *Store) ListLibraries(ctx context.Context, userID string, required store.LibraryRole) ([]store.AccessibleLibrary, error) {
	if err := checkLibraryRole(required); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+libraryColumns+`
		 FROM libraries l
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.owner_user_id = ? OR a.role = 'manage' OR (? = 'read' AND a.role = 'read')
		 ORDER BY l.created_at, l.id`,
		userID, userID, userID, string(required))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.AccessibleLibrary
	for rows.Next() {
		library, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, library)
	}
	return out, rows.Err()
}

// SetLibraryConfig replaces the configuration document of a library the
// actor manages. The UPDATE carries the ACL rather than checking it first,
// so a grant revoked between a check and a write cannot be raced.
//
// Last write wins. The document describes how the library should be read,
// not what it holds, and two administrators editing it in the same instant
// is not a case worth a revision column and a retry loop in every caller.
func (s *Store) SetLibraryConfig(ctx context.Context, actorUserID, libraryID string, configJSON []byte, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries
		 SET config_json = ?, updated_at = ?
		 WHERE id = ?
		   AND (owner_user_id = ?
		        OR EXISTS (SELECT 1 FROM library_access a
		                   WHERE a.library_id = libraries.id
		                     AND a.user_id = ? AND a.role = 'manage'))`,
		configJSON, formatTime(at), libraryID, actorUserID, actorUserID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) GrantLibraryAccess(ctx context.Context, actorUserID, libraryID, userID string, role store.LibraryRole, at time.Time) error {
	if err := checkLibraryRole(role); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO library_access (library_id, user_id, role, created_at)
		 SELECT l.id, ?, ?, ?
		 FROM libraries l
		 LEFT JOIN library_access actor
		   ON actor.library_id = l.id AND actor.user_id = ?
		 WHERE l.id = ? AND l.owner_user_id <> ?
		   AND (l.owner_user_id = ? OR actor.role = 'manage')
		 ON CONFLICT(library_id, user_id) DO UPDATE SET role = excluded.role`,
		userID, string(role), formatTime(at), actorUserID,
		libraryID, userID, actorUserID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) RevokeLibraryAccess(ctx context.Context, actorUserID, libraryID, userID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM library_access
		 WHERE library_id = ? AND user_id = ?
		   AND EXISTS (
		       SELECT 1
		       FROM libraries l
		       LEFT JOIN library_access actor
		         ON actor.library_id = l.id AND actor.user_id = ?
		       WHERE l.id = library_access.library_id
		         AND l.owner_user_id <> library_access.user_id
		         AND (l.owner_user_id = ? OR actor.role = 'manage')
		   )`,
		libraryID, userID, actorUserID, actorUserID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) CreateCatalogBook(ctx context.Context, actorUserID string, book store.CatalogBook) error {
	if book.UpdatedAt.IsZero() {
		book.UpdatedAt = book.CreatedAt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO books (
		     id, library_id, status,
		     title, title_source, title_locked,
		     subtitle, subtitle_source, subtitle_locked,
		     description, description_source, description_locked,
		     publisher, publisher_source, publisher_locked,
		     published_date, published_date_source, published_date_locked,
		     raw_metadata_json, created_at, updated_at, trashed_at, trash_expires_at,
		     identifiers_locked, languages_locked, tags_locked,
		     genres_locked, series_locked, contributors_locked
		 )
		 SELECT ?, l.id, ?,
		        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, ?, ?, ?,
		        ?, ?, ?, ?, ?, ?
		 FROM libraries l
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ? AND (l.owner_user_id = ? OR a.role = 'manage')`,
		book.ID, string(book.Status),
		book.Title, string(book.TitleSource), b2i(book.TitleLocked),
		book.Subtitle, string(book.SubtitleSource), b2i(book.SubtitleLocked),
		book.Description, string(book.DescriptionSource), b2i(book.DescriptionLocked),
		book.Publisher, string(book.PublisherSource), b2i(book.PublisherLocked),
		book.PublishedDate, string(book.PublishedDateSource), b2i(book.PublishedDateLocked),
		book.RawMetadataJSON, formatTime(book.CreatedAt), formatTime(book.UpdatedAt),
		formatTimePtr(book.TrashedAt), formatTimePtr(book.TrashExpiresAt),
		b2i(book.SetLocks.Identifiers), b2i(book.SetLocks.Languages),
		b2i(book.SetLocks.Tags), b2i(book.SetLocks.Genres),
		b2i(book.SetLocks.Series), b2i(book.SetLocks.Contributors),
		actorUserID, book.LibraryID, actorUserID)
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	// A book is findable from the moment it exists. Indexing it later
	// would mean a book that is listed and cannot be found by its title.
	if err := reindexBookTx(ctx, tx, book.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CatalogBookByID(ctx context.Context, userID, bookID string, required store.LibraryRole) (store.CatalogBook, error) {
	if err := checkLibraryRole(required); err != nil {
		return store.CatalogBook{}, err
	}
	book, err := scanCatalogBook(s.db.QueryRowContext(ctx,
		`SELECT `+bookColumns+`
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE b.id = ? AND b.status <> 'trashed'
		   AND (l.owner_user_id = ? OR a.role = 'manage' OR (? = 'read' AND a.role = 'read'))`,
		userID, bookID, userID, string(required)))
	if errors.Is(err, sql.ErrNoRows) {
		return book, store.ErrNotFound
	}
	return book, err
}

func (s *Store) ListCatalogBooks(
	ctx context.Context,
	userID, libraryID string,
	after *store.CatalogBookCursor,
	limit int,
) ([]store.CatalogBook, error) {
	return s.listCatalogBooks(ctx, userID, libraryID, after, limit, false)
}

// ListRecentCatalogBooks is the same listing read from the other end.
func (s *Store) ListRecentCatalogBooks(
	ctx context.Context,
	userID, libraryID string,
	before *store.CatalogBookCursor,
	limit int,
) ([]store.CatalogBook, error) {
	return s.listCatalogBooks(ctx, userID, libraryID, before, limit, true)
}

func (s *Store) listCatalogBooks(
	ctx context.Context,
	userID, libraryID string,
	cursor *store.CatalogBookCursor,
	limit int,
	newestFirst bool,
) ([]store.CatalogBook, error) {
	if limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleRead); err != nil {
		return nil, err
	}
	query := `SELECT ` + bookColumns + `
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ? AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))
		   AND b.status <> 'trashed'`
	args := []any{userID, libraryID, userID}
	// The comparison and the order have to move together, or a cursor
	// would skip the rows it was meant to resume before.
	compare, order := ">", ""
	if newestFirst {
		compare, order = "<", " DESC"
	}
	if cursor != nil {
		at := formatTime(cursor.CreatedAt)
		query += ` AND (b.created_at ` + compare + ` ? OR (b.created_at = ? AND b.id ` +
			compare + ` ?))`
		args = append(args, at, at, cursor.ID)
	}
	query += ` ORDER BY b.created_at` + order + `, b.id` + order + ` LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.CatalogBook
	for rows.Next() {
		book, err := scanCatalogBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, book)
	}
	return out, rows.Err()
}

// ListBookFiles is ACL-scoped through the book's library, like every other
// catalog read. Newest first, so a caller wanting "the" file can take the
// first available one without sorting.
func (s *Store) ListBookFiles(
	ctx context.Context,
	userID, bookID string,
	required store.LibraryRole,
) ([]store.BookFile, error) {
	if err := checkLibraryRole(required); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bookFileColumnsWithRoot+`
		 FROM book_files f
		 JOIN libraries l ON l.id = f.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE f.book_id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage' OR a.role = ?)
		 ORDER BY f.created_at DESC, f.id DESC`,
		userID, bookID, userID, string(required))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.BookFile
	for rows.Next() {
		file, err := scanBookFileWithRoot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, file)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		// Distinguishing "no such book" from "no access" would let a
		// caller probe for book ids.
		return nil, store.ErrNotFound
	}
	return out, nil
}

func (s *Store) ResolveCatalogBookWork(
	ctx context.Context,
	userID, bookID string,
	proposed store.Work,
	editions []store.Edition,
	ids []store.Identifier,
	confirmed bool,
	at time.Time,
) (store.WorkResolution, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.WorkResolution{}, err
	}
	defer tx.Rollback()
	if err := lockWorkGraph(ctx, tx, userID); err != nil {
		return store.WorkResolution{}, err
	}
	var libraryID string
	err = tx.QueryRowContext(ctx,
		`SELECT b.library_id
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE b.id = ? AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))`,
		userID, bookID, userID).
		Scan(&libraryID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.WorkResolution{}, store.ErrNotFound
	}
	if err != nil {
		return store.WorkResolution{}, err
	}
	result, err := resolveWorkTx(ctx, tx, userID, proposed, editions,
		store.CatalogWorkIdentifiers(bookID, ids), confirmed)
	if err != nil || len(result.ConflictingWorkIDs) > 0 || result.Confidence == "low" {
		return result, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO user_book_works
		 (user_id, library_id, book_id, work_id, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		userID, libraryID, bookID, result.WorkID, formatTime(at)); err != nil {
		return store.WorkResolution{}, err
	}
	var existing string
	if err := tx.QueryRowContext(ctx,
		`SELECT work_id FROM user_book_works WHERE user_id = ? AND book_id = ?`,
		userID, bookID).Scan(&existing); err != nil {
		return store.WorkResolution{}, err
	}
	if existing != result.WorkID {
		return store.WorkResolution{}, store.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return store.WorkResolution{}, err
	}
	return result, nil
}

func (s *Store) UserBookWork(ctx context.Context, userID, bookID string) (store.UserBookWork, error) {
	var mapping store.UserBookWork
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT m.user_id, m.library_id, m.book_id, m.work_id, m.created_at
		 FROM user_book_works m
		 JOIN libraries l ON l.id = m.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE m.user_id = ? AND m.book_id = ?
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))`,
		userID, userID, bookID, userID).
		Scan(&mapping.UserID, &mapping.LibraryID, &mapping.BookID, &mapping.WorkID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return mapping, store.ErrNotFound
	}
	if err != nil {
		return mapping, err
	}
	mapping.CreatedAt, err = parseTime(created)
	return mapping, err
}

func (s *Store) WorkBookIDs(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.work_id, m.book_id
		 FROM user_book_works m
		 JOIN libraries l ON l.id = m.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE m.user_id = ?
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))`,
		userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var workID, bookID string
		if err := rows.Scan(&workID, &bookID); err != nil {
			return nil, err
		}
		if _, ok := out[workID]; !ok {
			out[workID] = bookID
		}
	}
	return out, rows.Err()
}

// AvailableBookMediaTypes answers "which of these books could I be given
// right now, and in what format" for a whole list at once, so a page of
// books does not need a file query per row. Like every catalog read it
// is scoped through the library ACL: a book the user cannot read reports
// nothing rather than reporting that it exists.
func (s *Store) AvailableBookMediaTypes(
	ctx context.Context,
	userID string,
	bookIDs []string,
) (map[string][]string, error) {
	if len(bookIDs) == 0 {
		return map[string][]string{}, nil
	}
	placeholders := make([]string, len(bookIDs))
	args := []any{userID}
	for i, id := range bookIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, userID, string(store.BookFileAvailable))
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.book_id, f.media_type
		 FROM book_files f
		 JOIN libraries l ON l.id = f.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE f.book_id IN (`+strings.Join(placeholders, ",")+`)
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))
		   AND f.availability = ?
		 ORDER BY f.book_id, f.created_at DESC, f.id DESC`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]string)
	for rows.Next() {
		var bookID, mediaType string
		if err := rows.Scan(&bookID, &mediaType); err != nil {
			return nil, err
		}
		out[bookID] = append(out[bookID], mediaType)
	}
	return out, rows.Err()
}

func (s *Store) CatalogAuthorsForBooks(
	ctx context.Context,
	userID string,
	bookIDs []string,
) (map[string][]string, error) {
	if len(bookIDs) == 0 {
		return map[string][]string{}, nil
	}
	placeholders := make([]string, len(bookIDs))
	args := []any{userID}
	for i, id := range bookIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, userID, store.ContributorRoleAuthor)
	rows, err := s.db.QueryContext(ctx,
		`SELECT bc.book_id, c.name
		 FROM book_contributors bc
		 JOIN contributors c
		   ON c.library_id = bc.library_id AND c.id = bc.contributor_id
		 JOIN libraries l ON l.id = bc.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE bc.book_id IN (`+strings.Join(placeholders, ",")+`)
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))
		   AND bc.role = ?
		 ORDER BY bc.book_id, bc.position, c.normalized_name, c.id`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]string)
	for rows.Next() {
		var bookID, name string
		if err := rows.Scan(&bookID, &name); err != nil {
			return nil, err
		}
		out[bookID] = append(out[bookID], name)
	}
	return out, rows.Err()
}

// Metadata entity sets are read in one transaction and in a deterministic
// order, so a caller merging a proposal always sees a consistent snapshot
// and produces the same result for the same inputs.
const (
	bookIdentifierQuery = `SELECT scheme, value, source, locked
	 FROM book_identifiers
	 WHERE library_id = ? AND book_id = ?
	 ORDER BY scheme, value`
	bookLanguageQuery = `SELECT language, source, locked
	 FROM book_languages
	 WHERE library_id = ? AND book_id = ?
	 ORDER BY language`
	bookTagQuery = `SELECT t.id, t.name, t.normalized_name, bt.source, bt.locked
	 FROM book_tags bt
	 JOIN tags t ON t.library_id = bt.library_id AND t.id = bt.tag_id
	 WHERE bt.library_id = ? AND bt.book_id = ?
	 ORDER BY t.normalized_name, t.id`
	bookGenreQuery = `SELECT g.id, g.name, g.normalized_name, bg.source, bg.locked
	 FROM book_genres bg
	 JOIN genres g ON g.library_id = bg.library_id AND g.id = bg.genre_id
	 WHERE bg.library_id = ? AND bg.book_id = ?
	 ORDER BY g.normalized_name, g.id`
	bookSeriesQuery = `SELECT s.id, s.name, s.normalized_name, bs.position,
	        bs.source, bs.locked
	 FROM book_series bs
	 JOIN series s ON s.library_id = bs.library_id AND s.id = bs.series_id
	 WHERE bs.library_id = ? AND bs.book_id = ?
	 ORDER BY s.normalized_name, s.id`
	bookContributorQuery = `SELECT c.id, c.name, c.normalized_name, bc.role,
	        bc.position, bc.source, bc.locked
	 FROM book_contributors bc
	 JOIN contributors c
	   ON c.library_id = bc.library_id AND c.id = bc.contributor_id
	 WHERE bc.library_id = ? AND bc.book_id = ?
	 ORDER BY bc.role, bc.position, c.normalized_name, c.id`
)

func (s *Store) CatalogBookMetadata(
	ctx context.Context, userID, bookID string, required store.LibraryRole,
) (store.BookMetadata, error) {
	var out store.BookMetadata
	if err := checkLibraryRole(required); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = catalogBookMetadataTx(ctx, tx, userID, bookID, required)
	if err != nil {
		return store.BookMetadata{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.BookMetadata{}, err
	}
	return out, nil
}

func catalogBookMetadataTx(
	ctx context.Context, tx *sql.Tx, userID, bookID string,
	required store.LibraryRole,
) (store.BookMetadata, error) {
	var out store.BookMetadata
	book, err := scanCatalogBook(tx.QueryRowContext(ctx,
		`SELECT `+bookColumns+`
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE b.id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage' OR (? = 'read' AND a.role = 'read'))`,
		userID, bookID, userID, string(required)))
	if errors.Is(err, sql.ErrNoRows) {
		return out, store.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Book = book

	if err := queryRows(ctx, tx, bookIdentifierQuery, book.LibraryID, bookID,
		func(scan func(...any) error) error {
			var row store.BookIdentifier
			var locked int
			if err := scan(&row.Scheme, &row.Value, &row.Source, &locked); err != nil {
				return err
			}
			row.Locked = locked != 0
			out.Identifiers = append(out.Identifiers, row)
			return nil
		}); err != nil {
		return store.BookMetadata{}, err
	}
	if err := queryRows(ctx, tx, bookLanguageQuery, book.LibraryID, bookID,
		func(scan func(...any) error) error {
			var row store.BookLanguage
			var locked int
			if err := scan(&row.Language, &row.Source, &locked); err != nil {
				return err
			}
			row.Locked = locked != 0
			out.Languages = append(out.Languages, row)
			return nil
		}); err != nil {
		return store.BookMetadata{}, err
	}
	scanTaxon := func(target *[]store.BookTaxon) func(func(...any) error) error {
		return func(scan func(...any) error) error {
			var row store.BookTaxon
			var locked int
			if err := scan(&row.ID, &row.Name, &row.NormalizedName,
				&row.Source, &locked); err != nil {
				return err
			}
			row.Locked = locked != 0
			*target = append(*target, row)
			return nil
		}
	}
	if err := queryRows(ctx, tx, bookTagQuery, book.LibraryID, bookID,
		scanTaxon(&out.Tags)); err != nil {
		return store.BookMetadata{}, err
	}
	if err := queryRows(ctx, tx, bookGenreQuery, book.LibraryID, bookID,
		scanTaxon(&out.Genres)); err != nil {
		return store.BookMetadata{}, err
	}
	if err := queryRows(ctx, tx, bookSeriesQuery, book.LibraryID, bookID,
		func(scan func(...any) error) error {
			var row store.BookSeries
			var position sql.NullFloat64
			var locked int
			if err := scan(&row.SeriesID, &row.Name, &row.NormalizedName,
				&position, &row.Source, &locked); err != nil {
				return err
			}
			if position.Valid {
				value := position.Float64
				row.Position = &value
			}
			row.Locked = locked != 0
			out.Series = append(out.Series, row)
			return nil
		}); err != nil {
		return store.BookMetadata{}, err
	}
	if err := queryRows(ctx, tx, bookContributorQuery, book.LibraryID, bookID,
		func(scan func(...any) error) error {
			var row store.BookContributor
			var locked int
			if err := scan(&row.ContributorID, &row.Name, &row.NormalizedName,
				&row.Role, &row.Position, &row.Source, &locked); err != nil {
				return err
			}
			row.Locked = locked != 0
			out.Contributors = append(out.Contributors, row)
			return nil
		}); err != nil {
		return store.BookMetadata{}, err
	}
	return out, nil
}

func queryRows(
	ctx context.Context, tx *sql.Tx, query, libraryID, bookID string,
	each func(scan func(...any) error) error,
) error {
	rows, err := tx.QueryContext(ctx, query, libraryID, bookID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) ApplyCatalogBookMetadata(
	ctx context.Context, userID string, request store.ApplyBookMetadataRequest,
) (store.BookMetadata, error) {
	book := request.Metadata.Book
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.BookMetadata{}, err
	}
	defer tx.Rollback()

	// The revision-checked UPDATE runs first so concurrent applies to the
	// same book serialize on its row: the loser sees no updated row and
	// stops before touching any entity table.
	res, err := tx.ExecContext(ctx,
		`UPDATE books SET
		     title = ?, title_source = ?, title_locked = ?,
		     subtitle = ?, subtitle_source = ?, subtitle_locked = ?,
		     description = ?, description_source = ?, description_locked = ?,
		     publisher = ?, publisher_source = ?, publisher_locked = ?,
		     published_date = ?, published_date_source = ?, published_date_locked = ?,
		     identifiers_locked = ?, languages_locked = ?, tags_locked = ?,
		     genres_locked = ?, series_locked = ?, contributors_locked = ?,
		     updated_at = ?, revision = revision + 1
		 WHERE id = ? AND revision = ?
		   AND EXISTS (
		       SELECT 1 FROM libraries l
		       LEFT JOIN library_access a
		         ON a.library_id = l.id AND a.user_id = ?
		       WHERE l.id = books.library_id
		         AND (l.owner_user_id = ? OR a.role = 'manage')
		   )`,
		book.Title, string(book.TitleSource), b2i(book.TitleLocked),
		book.Subtitle, string(book.SubtitleSource), b2i(book.SubtitleLocked),
		book.Description, string(book.DescriptionSource), b2i(book.DescriptionLocked),
		book.Publisher, string(book.PublisherSource), b2i(book.PublisherLocked),
		book.PublishedDate, string(book.PublishedDateSource), b2i(book.PublishedDateLocked),
		b2i(book.SetLocks.Identifiers), b2i(book.SetLocks.Languages),
		b2i(book.SetLocks.Tags), b2i(book.SetLocks.Genres),
		b2i(book.SetLocks.Series), b2i(book.SetLocks.Contributors),
		formatTime(request.UpdatedAt), book.ID, request.ExpectedRevision,
		userID, userID)
	if err != nil {
		return store.BookMetadata{}, err
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return store.BookMetadata{}, err
	}
	if updated == 0 {
		// Either the caller cannot manage this book or another writer
		// already advanced it. Only a book the caller can manage is stale.
		if _, err := catalogBookMetadataTx(
			ctx, tx, userID, book.ID, store.LibraryRoleManage); err != nil {
			return store.BookMetadata{}, err
		}
		return store.BookMetadata{}, store.ErrStaleRevision
	}
	// The library id comes from the row, never from the request.
	current, err := catalogBookMetadataTx(
		ctx, tx, userID, book.ID, store.LibraryRoleManage)
	if err != nil {
		return store.BookMetadata{}, err
	}
	libraryID := current.Book.LibraryID

	if err := replaceBookMetadataSetsTx(
		ctx, tx, libraryID, book.ID, request); err != nil {
		return store.BookMetadata{}, err
	}
	out, err := catalogBookMetadataTx(
		ctx, tx, userID, book.ID, store.LibraryRoleManage)
	if err != nil {
		return store.BookMetadata{}, err
	}
	// The index is rebuilt in the same transaction as the edit, so a book
	// is findable by its new title the moment the edit that gave it one
	// commits. A search that lags behind the catalog lies about it.
	if err := reindexBookTx(ctx, tx, book.ID); err != nil {
		return store.BookMetadata{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.BookMetadata{}, err
	}
	return out, nil
}

func replaceBookMetadataSetsTx(
	ctx context.Context, tx *sql.Tx, libraryID, bookID string,
	request store.ApplyBookMetadataRequest,
) error {
	createdAt := formatTime(request.UpdatedAt)
	for _, table := range []string{
		"book_identifiers", "book_languages", "book_tags", "book_genres",
		"book_series", "book_contributors",
	} {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE library_id = ? AND book_id = ?`,
			libraryID, bookID); err != nil {
			return err
		}
	}
	entities, err := resolveMetadataEntitiesTx(ctx, tx, libraryID, request, createdAt)
	if err != nil {
		return err
	}
	for _, row := range request.Metadata.Identifiers {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_identifiers
			     (library_id, book_id, scheme, value, source, locked)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			libraryID, bookID, row.Scheme, row.Value,
			string(row.Source), b2i(row.Locked)); err != nil {
			return err
		}
	}
	for _, row := range request.Metadata.Languages {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_languages
			     (library_id, book_id, language, source, locked)
			 VALUES (?, ?, ?, ?, ?)`,
			libraryID, bookID, row.Language,
			string(row.Source), b2i(row.Locked)); err != nil {
			return err
		}
	}
	for _, row := range request.Metadata.Tags {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_tags (library_id, book_id, tag_id, source, locked)
			 VALUES (?, ?, ?, ?, ?)`,
			libraryID, bookID, entities[entityKey("tags", row.NormalizedName)],
			string(row.Source), b2i(row.Locked)); err != nil {
			return err
		}
	}
	for _, row := range request.Metadata.Genres {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_genres (library_id, book_id, genre_id, source, locked)
			 VALUES (?, ?, ?, ?, ?)`,
			libraryID, bookID, entities[entityKey("genres", row.NormalizedName)],
			string(row.Source), b2i(row.Locked)); err != nil {
			return err
		}
	}
	for _, row := range request.Metadata.Series {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_series
			     (library_id, book_id, series_id, position, source, locked)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			libraryID, bookID, entities[entityKey("series", row.NormalizedName)],
			row.Position, string(row.Source), b2i(row.Locked)); err != nil {
			return err
		}
	}
	for _, row := range request.Metadata.Contributors {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_contributors
			     (library_id, book_id, contributor_id, role, position, source, locked)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			libraryID, bookID,
			entities[entityKey("contributors", row.NormalizedName)],
			row.Role, row.Position,
			string(row.Source), b2i(row.Locked)); err != nil {
			return err
		}
	}
	return nil
}

func entityKey(table, normalizedName string) string {
	return table + "\x00" + normalizedName
}

// resolveMetadataEntitiesTx resolves every entity this request needs before
// any of them is inserted, in one canonical order. Two applies to different
// books that create the same new names must take their insertion locks in
// the same order, or PostgreSQL deadlocks one of them.
func resolveMetadataEntitiesTx(
	ctx context.Context, tx *sql.Tx, libraryID string,
	request store.ApplyBookMetadataRequest, createdAt string,
) (map[string]string, error) {
	type entityRequest struct{ table, candidateID, name, normalizedName string }
	var wanted []entityRequest
	for _, row := range request.Metadata.Tags {
		wanted = append(wanted, entityRequest{
			"tags", row.ID, row.Name, row.NormalizedName})
	}
	for _, row := range request.Metadata.Genres {
		wanted = append(wanted, entityRequest{
			"genres", row.ID, row.Name, row.NormalizedName})
	}
	for _, row := range request.Metadata.Series {
		wanted = append(wanted, entityRequest{
			"series", row.SeriesID, row.Name, row.NormalizedName})
	}
	for _, row := range request.Metadata.Contributors {
		wanted = append(wanted, entityRequest{
			"contributors", row.ContributorID, row.Name, row.NormalizedName})
	}
	sort.Slice(wanted, func(i, j int) bool {
		if wanted[i].table != wanted[j].table {
			return wanted[i].table < wanted[j].table
		}
		return wanted[i].normalizedName < wanted[j].normalizedName
	})
	resolved := make(map[string]string, len(wanted))
	for _, want := range wanted {
		key := entityKey(want.table, want.normalizedName)
		if _, done := resolved[key]; done {
			continue
		}
		id, err := resolveMetadataEntityTx(ctx, tx, want.table, libraryID,
			want.candidateID, want.name, want.normalizedName, createdAt)
		if err != nil {
			return nil, err
		}
		resolved[key] = id
	}
	return resolved, nil
}

// resolveMetadataEntityTx returns the id of the library's entity with this
// normalized name, creating it with the caller's candidate id when it does
// not exist yet. Display spelling of an existing entity is left alone: the
// first spelling wins until an explicit rename, so a rescan cannot flip a
// shared entity's name under every other book that references it.
//
// The candidate id is only a proposal. Entity ids are unique table-wide, so
// one that is already taken by another name is a caller conflict, not a
// reason to fail the request with a driver error.
func resolveMetadataEntityTx(
	ctx context.Context, tx *sql.Tx, table, libraryID, candidateID, name,
	normalizedName, createdAt string,
) (string, error) {
	id, err := lookupMetadataEntityTx(ctx, tx, table, libraryID, normalizedName)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+table+` (id, library_id, name, normalized_name, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (library_id, normalized_name) DO NOTHING`,
		candidateID, libraryID, name, normalizedName, createdAt); err != nil {
		if isUniqueErr(err) {
			return "", store.ErrConflict
		}
		return "", err
	}
	id, err = lookupMetadataEntityTx(ctx, tx, table, libraryID, normalizedName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrConflict
	}
	return id, err
}

func lookupMetadataEntityTx(
	ctx context.Context, tx *sql.Tx, table, libraryID, normalizedName string,
) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM `+table+` WHERE library_id = ? AND normalized_name = ?`,
		libraryID, normalizedName).Scan(&id)
	return id, err
}

// ListTrashedBooks is the trash view. It is manage-only and deliberately
// separate from ListCatalogBooks: the catalog is what a reader browses,
// and a deleted book is not part of it. Most recently trashed first,
// because that is the one an operator is most likely to want back.
func (s *Store) ListTrashedBooks(
	ctx context.Context,
	userID, libraryID string,
	limit int,
) ([]store.CatalogBook, error) {
	if limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleManage); err != nil {
		return nil, err
	}
	// The ACL is repeated inside the query, as in every other catalog
	// read here, even though the check above already settled it. It is
	// deliberate belt and braces: a query that can only return rows the
	// caller may see stays safe if a future caller forgets the gate.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bookColumns+`
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ? AND b.status = 'trashed'
		   AND (l.owner_user_id = ? OR a.role = 'manage')
		 ORDER BY b.trashed_at DESC, b.id LIMIT ?`,
		userID, libraryID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.CatalogBook
	for rows.Next() {
		book, err := scanCatalogBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, book)
	}
	return out, rows.Err()
}

func (s *Store) ListDuplicateContentBooks(
	ctx context.Context,
	userID, libraryID string,
	limit int,
) ([]store.DuplicateContentBook, error) {
	if limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleRead); err != nil {
		return nil, err
	}
	// Only active books count. A trashed book still references its blob —
	// that is what makes restore a relink — but reporting it as a
	// duplicate would tell the user to resolve something they already
	// resolved by deleting it.
	//
	// The ACL is repeated inside the query, as in every other catalog read
	// here, even though the check above already settled it.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bookColumns+`, f.content_sha256
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 JOIN book_files f ON f.book_id = b.id
		 WHERE l.id = ? AND b.status = 'active'
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))
		   AND EXISTS (
		     SELECT 1 FROM book_files o
		     JOIN books ob ON ob.id = o.book_id
		     WHERE o.content_sha256 = f.content_sha256
		       AND ob.library_id = b.library_id
		       AND ob.status = 'active'
		       AND o.book_id <> f.book_id)
		 GROUP BY b.id, f.content_sha256
		 ORDER BY f.content_sha256, b.created_at, b.id LIMIT ?`,
		userID, libraryID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.DuplicateContentBook
	for rows.Next() {
		duplicate, err := scanDuplicateContentBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, duplicate)
	}
	return out, rows.Err()
}

// appendedScan lets a row carrying one extra trailing column reuse the
// book scanner rather than restate thirty fields that would then have to
// be kept in step with it.
type appendedScan struct {
	row   interface{ Scan(...any) error }
	extra []any
}

func (a appendedScan) Scan(dest ...any) error {
	return a.row.Scan(append(dest, a.extra...)...)
}

func scanDuplicateContentBook(
	row interface{ Scan(...any) error },
) (store.DuplicateContentBook, error) {
	var sha string
	book, err := scanCatalogBook(appendedScan{row: row, extra: []any{&sha}})
	if err != nil {
		return store.DuplicateContentBook{}, err
	}
	return store.DuplicateContentBook{Book: book, SHA256: sha}, nil
}
