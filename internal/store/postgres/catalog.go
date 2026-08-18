package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// bookColumns is the full projection of a catalog book. Every read of a
// book goes through it and through scanCatalogBook, so a column added to
// the table is added in exactly one place.
const bookColumns = `b.id, b.folder_id, b.status,
	b.relative_path, b.size_bytes, b.mtime, b.content_sha256,
	b.original_filename, b.media_type, b.calibre_id,
	b.cover_relative_path, b.cover_sha256,
	b.title, b.subtitle, b.description, b.publisher, b.published_date,
	b.created_at, b.updated_at, b.seen_at, b.absent_at`

func scanCatalogBook(row interface{ Scan(...any) error }) (store.CatalogBook, error) {
	var (
		b         store.CatalogBook
		status    string
		calibreID sql.NullInt64
		coverPath sql.NullString
		coverSHA  sql.NullString
	)
	if err := row.Scan(
		&b.ID, &b.FolderID, &status,
		&b.RelativePath, &b.SizeBytes, &b.MTime, &b.ContentSHA256,
		&b.OriginalFilename, &b.MediaType, &calibreID,
		&coverPath, &coverSHA,
		&b.Title, &b.Subtitle, &b.Description, &b.Publisher, &b.PublishedDate,
		&b.CreatedAt, &b.UpdatedAt, &b.SeenAt, &b.AbsentAt,
	); err != nil {
		return store.CatalogBook{}, err
	}
	b.Status = store.BookStatus(status)
	if calibreID.Valid {
		id := calibreID.Int64
		b.CalibreID = &id
	}
	if coverPath.Valid {
		p := coverPath.String
		b.CoverRelativePath = &p
	}
	b.CoverSHA256 = coverSHA.String
	b.MTime = b.MTime.UTC()
	b.CreatedAt = b.CreatedAt.UTC()
	b.UpdatedAt = b.UpdatedAt.UTC()
	utcPtr(b.SeenAt)
	utcPtr(b.AbsentAt)
	return b, nil
}

// utcPtr normalises a nullable timestamp in place. Postgres hands back
// times in the session's zone; every caller compares against UTC.
func utcPtr(t *time.Time) {
	if t != nil {
		*t = t.UTC()
	}
}

func (s *Store) CatalogBookByID(ctx context.Context, bookID string) (store.CatalogBook, error) {
	row := s.db.QueryRowContext(ctx,
		q(`SELECT `+bookColumns+` FROM books b WHERE b.id = ?`), bookID)
	book, err := scanCatalogBook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.CatalogBook{}, store.ErrNotFound
	}
	return book, err
}

// CatalogBookByDigest finds an active book by content digest. Two
// folders may hold the same bytes; the oldest row wins, so the answer
// does not change as the catalog grows.
func (s *Store) CatalogBookByDigest(
	ctx context.Context, sha string,
) (store.CatalogBook, error) {
	row := s.db.QueryRowContext(ctx, q(`SELECT `+bookColumns+`
		   FROM books b
		  WHERE b.content_sha256 = ? AND b.status = 'active'
		  ORDER BY b.created_at, b.id
		  LIMIT 1`), sha)
	book, err := scanCatalogBook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.CatalogBook{}, store.ErrNotFound
	}
	return book, err
}

func (s *Store) ListCatalogBooks(
	ctx context.Context, folderID string, after *store.CatalogBookCursor, limit int,
) ([]store.CatalogBook, error) {
	return s.listCatalogBooks(ctx, folderID, after, limit, true)
}

func (s *Store) ListRecentCatalogBooks(
	ctx context.Context, folderID string, before *store.CatalogBookCursor, limit int,
) ([]store.CatalogBook, error) {
	return s.listCatalogBooks(ctx, folderID, before, limit, false)
}

// listCatalogBooks pages a folder's books in either direction. Only
// active books are listed. A missing book is one this server will refuse
// to serve — its download is a 410 and so is its cover — so listing it
// would be advertising something unopenable. It keeps its row and its
// readers' work mappings, and it returns to the listing whole the moment
// a complete pass sees its file again.
func (s *Store) listCatalogBooks(
	ctx context.Context, folderID string, cursor *store.CatalogBookCursor,
	limit int, ascending bool,
) ([]store.CatalogBook, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT ` + bookColumns + ` FROM books b
		 WHERE b.folder_id = ? AND b.status = 'active'`
	args := []any{folderID}
	if cursor != nil {
		if ascending {
			query += ` AND (b.created_at, b.id) > (?, ?)`
		} else {
			query += ` AND (b.created_at, b.id) < (?, ?)`
		}
		args = append(args, cursor.CreatedAt.UTC(), cursor.ID)
	}
	if ascending {
		query += ` ORDER BY b.created_at, b.id`
	} else {
		query += ` ORDER BY b.created_at DESC, b.id DESC`
	}
	query += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	books := []store.CatalogBook{}
	for rows.Next() {
		book, err := scanCatalogBook(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, book)
	}
	return books, rows.Err()
}

// CatalogBookRelationsForBooks reads a whole page's contributors and
// series in bounded batched queries rather than per book (ADR-0015): a
// shelf of fifty books never costs a hundred round trips.
func (s *Store) CatalogBookRelationsForBooks(
	ctx context.Context, userID string, bookIDs []string,
) (store.CatalogBookRelations, error) {
	out := store.CatalogBookRelations{
		Contributors:         map[string][]store.BookContributor{},
		Series:               map[string][]store.BookSeries{},
		SeriesSource:         map[string]store.SeriesSource{},
		SeriesClaimUpdatedAt: map[string]*time.Time{},
	}
	if len(bookIDs) == 0 {
		return out, nil
	}
	placeholders, args := inArgs(bookIDs)
	readerScope := userID
	if readerScope == "" {
		readerScope = store.NoReaderScope
	}
	for _, bookID := range bookIDs {
		out.SeriesSource[bookID] = store.SeriesSourceFolder
	}

	rows, err := s.db.QueryContext(ctx, q(
		`SELECT bc.book_id, c.id, c.name, c.normalized_name, bc.role, bc.position
		 FROM book_contributors bc
		 JOIN contributors c ON c.id = bc.contributor_id
		 WHERE bc.book_id IN (`+placeholders+`)
		 ORDER BY bc.book_id, bc.role, bc.position, c.normalized_name, c.id`),
		args...)
	if err != nil {
		return out, err
	}
	if err := eachRow(rows, func(scan func(...any) error) error {
		var bookID string
		var c store.BookContributor
		if err := scan(&bookID, &c.ContributorID, &c.Name, &c.NormalizedName,
			&c.Role, &c.Position); err != nil {
			return err
		}
		out.Contributors[bookID] = append(out.Contributors[bookID], c)
		return nil
	}); err != nil {
		return out, err
	}

	// Series resolve through the reader's override layers (ADR-0018),
	// so this one relation is asked on behalf of somebody. The rest of
	// the catalog is shared and is not.
	rows, err = s.db.QueryContext(ctx, q(
		effectiveSeriesCTE+
			`SELECT e.book_id, n.series_id, n.name, n.normalized_name,
			        e.position, e.source
			 FROM eff_series e
			 JOIN series_names n ON n.series_id = e.series_id
			 WHERE e.book_id IN (`+placeholders+`)
			 ORDER BY e.book_id, n.normalized_name, n.series_id`),
		seriesReadArgs(userID, args...)...)
	if err != nil {
		return out, err
	}
	if err := eachRow(rows, func(scan func(...any) error) error {
		var bookID string
		var sr store.BookSeries
		var position sql.NullFloat64
		if err := scan(&bookID, &sr.SeriesID, &sr.Name, &sr.NormalizedName,
			&position, &sr.Source); err != nil {
			return err
		}
		if position.Valid {
			p := position.Float64
			sr.Position = &p
		}
		out.Series[bookID] = append(out.Series[bookID], sr)
		return nil
	}); err != nil {
		return out, err
	}

	// A claim can deliberately contain no memberships, so its source and
	// revision cannot be derived from the effective membership rows.
	rows, err = s.db.QueryContext(ctx, q(`SELECT book_id, scope_user, updated_at
		FROM book_series_overrides
		WHERE book_id IN (`+placeholders+`) AND scope_user IN ('', ?)
		AND deleted_at IS NULL`), append(args, readerScope)...)
	if err != nil {
		return out, err
	}
	if err := eachRow(rows, func(scan func(...any) error) error {
		var bookID, scopeUser string
		var updatedAt time.Time
		if err := scan(&bookID, &scopeUser, &updatedAt); err != nil {
			return err
		}
		shared := scopeUser == store.SharedSeriesScope &&
			out.SeriesSource[bookID] == store.SeriesSourceFolder
		if scopeUser == readerScope || shared {
			at := updatedAt.UTC()
			out.SeriesSource[bookID] = store.SeriesSourceShared
			// Not scopeUser == userID: a signed-out caller has a userID of
			// "", which is also the shared scope, and would be handed every
			// other reader's shared claim as if it were their own.
			if userID != "" && scopeUser == readerScope {
				out.SeriesSource[bookID] = store.SeriesSourcePersonal
			}
			out.SeriesClaimUpdatedAt[bookID] = &at
		}
		return nil
	}); err != nil {
		return out, err
	}
	return out, nil
}

// CatalogAuthorsForBooks feeds the identity backfill that links a
// catalog book to a sync work, which needs a title and its authors and
// nothing else.
func (s *Store) CatalogAuthorsForBooks(
	ctx context.Context, bookIDs []string,
) (map[string][]string, error) {
	out := map[string][]string{}
	if len(bookIDs) == 0 {
		return out, nil
	}
	placeholders, args := inArgs(bookIDs)
	args = append(args, store.ContributorRoleAuthor)
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT bc.book_id, c.name
		 FROM book_contributors bc
		 JOIN contributors c ON c.id = bc.contributor_id
		 WHERE bc.book_id IN (`+placeholders+`) AND bc.role = ?
		 ORDER BY bc.book_id, bc.position, c.normalized_name`),
		args...)
	if err != nil {
		return nil, err
	}
	if err := eachRow(rows, func(scan func(...any) error) error {
		var bookID, name string
		if err := scan(&bookID, &name); err != nil {
			return err
		}
		out[bookID] = append(out[bookID], name)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) AvailableBookMediaTypes(ctx context.Context, folderID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT DISTINCT media_type FROM books
		 WHERE folder_id = ? AND status = 'active' AND media_type <> ''
		 ORDER BY media_type`), folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		types = append(types, t)
	}
	return types, rows.Err()
}

// ResolveCatalogBookWork joins a shared catalog book to one of the
// caller's own works, in one transaction, so that the work resolution
// and the mapping cannot disagree.
//
// The catalog is shared; this mapping never is. Two readers of the same
// file get two work ids and neither can observe the other's.
//
// A low-confidence or conflicting resolution links nothing: guessing
// would attach one reader's history to another book, which is the one
// mistake here that cannot be undone from the outside.
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
	var folderID string
	err = tx.QueryRowContext(ctx, q(`SELECT folder_id FROM books WHERE id = ?`), bookID).Scan(&folderID)
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
	if _, err := tx.ExecContext(ctx, q(`INSERT INTO user_book_works
		 (user_id, folder_id, book_id, work_id, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, book_id) DO NOTHING`),
		userID, folderID, bookID, result.WorkID, at.UTC()); err != nil {
		return store.WorkResolution{}, err
	}
	// A book already mapped to a different work is a conflict rather
	// than a silent re-point: the reader's positions are recorded
	// against the work they already have.
	var existing string
	if err := tx.QueryRowContext(ctx, q(`SELECT work_id FROM user_book_works WHERE user_id = ? AND book_id = ?`),
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

func (s *Store) UserBookWork(
	ctx context.Context, userID, bookID string,
) (store.UserBookWork, error) {
	row := s.db.QueryRowContext(ctx, q(
		`SELECT user_id, folder_id, book_id, work_id, created_at
		 FROM user_book_works WHERE user_id = ? AND book_id = ?`), userID, bookID)
	out, err := scanUserBookWork(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.UserBookWork{}, store.ErrNotFound
	}
	return out, err
}

func scanUserBookWork(row interface{ Scan(...any) error }) (store.UserBookWork, error) {
	var out store.UserBookWork
	if err := row.Scan(&out.UserID, &out.FolderID, &out.BookID, &out.WorkID,
		&out.CreatedAt); err != nil {
		return store.UserBookWork{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}

func (s *Store) WorkBookIDs(ctx context.Context, userID, workID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT book_id FROM user_book_works
		 WHERE user_id = ? AND work_id = ? ORDER BY book_id`), userID, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// inArgs builds an IN list of ? placeholders, which q rewrites to $N.
// Ids are always bound, never interpolated.
func inArgs(ids []string) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return strings.Join(placeholders, ","), args
}

func eachRow(rows *sql.Rows, each func(scan func(...any) error) error) error {
	defer rows.Close()
	for rows.Next() {
		if err := each(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

// CatalogBookIdentifiers reads the publication identifiers a pass found
// in one book — its dc:identifier entries and whatever Calibre records.
// They are the strongest evidence for joining a catalog book to a work
// that is not the file's own digest.
func (s *Store) CatalogBookIdentifiers(
	ctx context.Context, bookID string,
) ([]store.BookIdentifier, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT scheme, value FROM book_identifiers
		 WHERE book_id = ? ORDER BY scheme, value`), bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.BookIdentifier{}
	for rows.Next() {
		var id store.BookIdentifier
		if err := rows.Scan(&id.Scheme, &id.Value); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
