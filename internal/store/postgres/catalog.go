package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func scanLibrary(row interface{ Scan(...any) error }) (store.AccessibleLibrary, error) {
	var out store.AccessibleLibrary
	err := row.Scan(
		&out.Library.ID,
		&out.Library.OwnerUserID,
		&out.Library.QuotaUserID,
		&out.Library.Kind,
		&out.Library.Name,
		&out.Library.RootPath,
		&out.Library.ConfigJSON,
		&out.Library.CreatedAt,
		&out.Library.UpdatedAt,
		&out.Role,
	)
	return out, err
}

func scanCatalogBook(row interface{ Scan(...any) error }) (store.CatalogBook, error) {
	var book store.CatalogBook
	err := row.Scan(
		&book.ID,
		&book.LibraryID,
		&book.Status,
		&book.Title,
		&book.TitleSource,
		&book.TitleLocked,
		&book.Subtitle,
		&book.SubtitleSource,
		&book.SubtitleLocked,
		&book.Description,
		&book.DescriptionSource,
		&book.DescriptionLocked,
		&book.Publisher,
		&book.PublisherSource,
		&book.PublisherLocked,
		&book.PublishedDate,
		&book.PublishedDateSource,
		&book.PublishedDateLocked,
		&book.RawMetadataJSON,
		&book.CreatedAt,
		&book.UpdatedAt,
		&book.TrashedAt,
		&book.TrashExpiresAt,
	)
	return book, err
}

const libraryColumns = `l.id, l.owner_user_id, l.quota_user_id, l.kind, l.name,
	l.root_path, l.config_json, l.created_at, l.updated_at,
	CASE WHEN l.owner_user_id = ? THEN 'manage' ELSE a.role END`

const bookColumns = `b.id, b.library_id, b.status,
	b.title, b.title_source, b.title_locked,
	b.subtitle, b.subtitle_source, b.subtitle_locked,
	b.description, b.description_source, b.description_locked,
	b.publisher, b.publisher_source, b.publisher_locked,
	b.published_date, b.published_date_source, b.published_date_locked,
	b.raw_metadata_json, b.created_at, b.updated_at, b.trashed_at, b.trash_expires_at`

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
	_, err := s.db.ExecContext(ctx, q(
		`INSERT INTO libraries
		 (id, owner_user_id, quota_user_id, kind, name, root_path, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		library.ID, library.OwnerUserID, library.QuotaUserID, string(library.Kind),
		library.Name, library.RootPath, library.ConfigJSON,
		library.CreatedAt.UTC(), library.UpdatedAt.UTC())
	if isUniqueErr(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) LibraryByID(ctx context.Context, userID, libraryID string, required store.LibraryRole) (store.AccessibleLibrary, error) {
	if err := checkLibraryRole(required); err != nil {
		return store.AccessibleLibrary{}, err
	}
	out, err := scanLibrary(s.db.QueryRowContext(ctx, q(
		`SELECT `+libraryColumns+`
		 FROM libraries l
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage' OR (? = 'read' AND a.role = 'read'))`),
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
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+libraryColumns+`
		 FROM libraries l
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.owner_user_id = ? OR a.role = 'manage' OR (? = 'read' AND a.role = 'read')
		 ORDER BY l.created_at, l.id`),
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

func (s *Store) GrantLibraryAccess(ctx context.Context, actorUserID, libraryID, userID string, role store.LibraryRole, at time.Time) error {
	if err := checkLibraryRole(role); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, q(
		`INSERT INTO library_access (library_id, user_id, role, created_at)
		 SELECT l.id, ?, ?, ?
		 FROM libraries l
		 LEFT JOIN library_access actor
		   ON actor.library_id = l.id AND actor.user_id = ?
		 WHERE l.id = ? AND l.owner_user_id <> ?
		   AND (l.owner_user_id = ? OR actor.role = 'manage')
		 ON CONFLICT(library_id, user_id) DO UPDATE SET role = excluded.role`),
		userID, string(role), at.UTC(), actorUserID,
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
	res, err := s.db.ExecContext(ctx, q(
		`DELETE FROM library_access target
		 WHERE target.library_id = ? AND target.user_id = ?
		   AND EXISTS (
		       SELECT 1
		       FROM libraries l
		       LEFT JOIN library_access actor
		         ON actor.library_id = l.id AND actor.user_id = ?
		       WHERE l.id = target.library_id
		         AND l.owner_user_id <> target.user_id
		         AND (l.owner_user_id = ? OR actor.role = 'manage')
		   )`),
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
	res, err := s.db.ExecContext(ctx, q(
		`INSERT INTO books (
		     id, library_id, status,
		     title, title_source, title_locked,
		     subtitle, subtitle_source, subtitle_locked,
		     description, description_source, description_locked,
		     publisher, publisher_source, publisher_locked,
		     published_date, published_date_source, published_date_locked,
		     raw_metadata_json, created_at, updated_at, trashed_at, trash_expires_at
		 )
		 SELECT ?, l.id, ?,
		        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, ?, ?, ?
		 FROM libraries l
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ? AND (l.owner_user_id = ? OR a.role = 'manage')`),
		book.ID, string(book.Status),
		book.Title, string(book.TitleSource), book.TitleLocked,
		book.Subtitle, string(book.SubtitleSource), book.SubtitleLocked,
		book.Description, string(book.DescriptionSource), book.DescriptionLocked,
		book.Publisher, string(book.PublisherSource), book.PublisherLocked,
		book.PublishedDate, string(book.PublishedDateSource), book.PublishedDateLocked,
		book.RawMetadataJSON, book.CreatedAt.UTC(), book.UpdatedAt.UTC(),
		book.TrashedAt, book.TrashExpiresAt,
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
	return nil
}

func (s *Store) CatalogBookByID(ctx context.Context, userID, bookID string, required store.LibraryRole) (store.CatalogBook, error) {
	if err := checkLibraryRole(required); err != nil {
		return store.CatalogBook{}, err
	}
	book, err := scanCatalogBook(s.db.QueryRowContext(ctx, q(
		`SELECT `+bookColumns+`
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE b.id = ?
		   AND (l.owner_user_id = ? OR a.role = 'manage' OR (? = 'read' AND a.role = 'read'))`),
		userID, bookID, userID, string(required)))
	if errors.Is(err, sql.ErrNoRows) {
		return book, store.ErrNotFound
	}
	return book, err
}

func (s *Store) ListCatalogBooks(ctx context.Context, userID, libraryID string) ([]store.CatalogBook, error) {
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleRead); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT `+bookColumns+`
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ? AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))
		 ORDER BY b.created_at, b.id`),
		userID, libraryID, userID)
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
	err = tx.QueryRowContext(ctx, q(
		`SELECT b.library_id
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE b.id = ? AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))`),
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
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO user_book_works
		 (user_id, library_id, book_id, work_id, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, book_id) DO NOTHING`),
		userID, libraryID, bookID, result.WorkID, at.UTC()); err != nil {
		return store.WorkResolution{}, err
	}
	var existing string
	if err := tx.QueryRowContext(ctx, q(
		`SELECT work_id FROM user_book_works WHERE user_id = ? AND book_id = ?`),
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
	err := s.db.QueryRowContext(ctx, q(
		`SELECT m.user_id, m.library_id, m.book_id, m.work_id, m.created_at
		 FROM user_book_works m
		 JOIN libraries l ON l.id = m.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE m.user_id = ? AND m.book_id = ?
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))`),
		userID, userID, bookID, userID).
		Scan(&mapping.UserID, &mapping.LibraryID, &mapping.BookID,
			&mapping.WorkID, &mapping.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return mapping, store.ErrNotFound
	}
	return mapping, err
}
