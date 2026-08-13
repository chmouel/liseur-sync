package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// entityTables names the two tables one kind lives in and the column that
// joins them. Selecting these from a closed set is what lets the queries
// below be built with string concatenation without ever putting a
// caller's input into SQL.
type entityTables struct {
	entity     string
	membership string
	column     string
}

func tablesFor(kind store.EntityKind) (entityTables, error) {
	switch kind {
	case store.EntitySeries:
		return entityTables{"series", "book_series", "series_id"}, nil
	case store.EntityContributor:
		return entityTables{"contributors", "book_contributors", "contributor_id"}, nil
	case store.EntityTag:
		return entityTables{"tags", "book_tags", "tag_id"}, nil
	case store.EntityGenre:
		return entityTables{"genres", "book_genres", "genre_id"}, nil
	default:
		return entityTables{}, store.ErrInvalidTransition
	}
}

func (s *Store) ListCatalogEntities(
	ctx context.Context,
	userID, libraryID string,
	kind store.EntityKind,
	after string,
	limit int,
) ([]store.CatalogEntity, error) {
	if limit < 1 || limit > store.MaxEntityListLimit {
		return nil, store.ErrInvalidTransition
	}
	tables, err := tablesFor(kind)
	if err != nil {
		return nil, err
	}
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleRead); err != nil {
		return nil, err
	}
	// Only active books are counted, so an entity whose books are all in
	// the trash reads as empty rather than as a populated entity whose
	// page is blank.
	//
	// The ACL is repeated inside the query, as in every other catalog
	// read here, so it stays safe if a future caller forgets the gate.
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.name, e.normalized_name, e.created_at,
		        (SELECT COUNT(*) FROM `+tables.membership+` m
		         JOIN books b ON b.id = m.book_id
		         WHERE m.`+tables.column+` = e.id AND b.status = 'active')
		 FROM `+tables.entity+` e
		 JOIN libraries l ON l.id = e.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ? AND e.normalized_name > ?
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))
		 ORDER BY e.normalized_name LIMIT ?`,
		userID, libraryID, after, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.CatalogEntity
	for rows.Next() {
		entity := store.CatalogEntity{Kind: kind}
		var created string
		if err := rows.Scan(&entity.ID, &entity.Name, &entity.NormalizedName,
			&created, &entity.BookCount); err != nil {
			return nil, err
		}
		if entity.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, entity)
	}
	return out, rows.Err()
}

func (s *Store) CatalogEntityByID(
	ctx context.Context,
	userID, libraryID, entityID string,
	kind store.EntityKind,
) (store.CatalogEntity, error) {
	tables, err := tablesFor(kind)
	if err != nil {
		return store.CatalogEntity{}, err
	}
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleRead); err != nil {
		return store.CatalogEntity{}, err
	}
	entity := store.CatalogEntity{Kind: kind}
	var created string
	err = s.db.QueryRowContext(ctx,
		`SELECT e.id, e.name, e.normalized_name, e.created_at,
		        (SELECT COUNT(*) FROM `+tables.membership+` m
		         JOIN books b ON b.id = m.book_id
		         WHERE m.`+tables.column+` = e.id AND b.status = 'active')
		 FROM `+tables.entity+` e
		 JOIN libraries l ON l.id = e.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ? AND e.id = ?
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))`,
		userID, libraryID, entityID, userID).
		Scan(&entity.ID, &entity.Name, &entity.NormalizedName,
			&created, &entity.BookCount)
	if errors.Is(err, sql.ErrNoRows) {
		return store.CatalogEntity{}, store.ErrNotFound
	}
	if err != nil {
		return store.CatalogEntity{}, err
	}
	entity.CreatedAt, err = parseTime(created)
	return entity, err
}

func (s *Store) ListBooksByEntity(
	ctx context.Context,
	userID, libraryID, entityID string,
	kind store.EntityKind,
	after *store.CatalogBookCursor,
	limit int,
) ([]store.CatalogBook, error) {
	if limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	tables, err := tablesFor(kind)
	if err != nil {
		return nil, err
	}
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleRead); err != nil {
		return nil, err
	}
	// A series is the one kind with an order of its own, and it is the
	// order a reader wants: book three of a trilogy is not interesting
	// for having been uploaded first. Books with no position sort last,
	// because an unplaced book is an unanswered question rather than
	// book zero.
	order := "b.created_at, b.id"
	if kind == store.EntitySeries {
		order = "m.position IS NULL, m.position, b.created_at, b.id"
	}
	args := []any{userID, libraryID, entityID, userID}
	cursor := ""
	if after != nil {
		cursor = " AND (b.created_at, b.id) > (?, ?)"
		args = append(args, formatTime(after.CreatedAt), after.ID)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bookColumns+`
		 FROM books b
		 JOIN `+tables.membership+` m ON m.book_id = b.id
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ? AND m.`+tables.column+` = ? AND b.status = 'active'
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))`+
			cursor+` ORDER BY `+order+` LIMIT ?`, args...)
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

func (s *Store) RenameCatalogEntity(
	ctx context.Context,
	userID, libraryID, entityID string,
	kind store.EntityKind,
	name string,
) (store.CatalogEntity, error) {
	tables, err := tablesFor(kind)
	if err != nil {
		return store.CatalogEntity{}, err
	}
	display := strings.TrimSpace(name)
	normalized := metadata.NormalizeName(display)
	if normalized == "" {
		return store.CatalogEntity{}, store.ErrInvalidTransition
	}
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleManage); err != nil {
		return store.CatalogEntity{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.CatalogEntity{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE `+tables.entity+` SET name = ?, normalized_name = ?
		 WHERE id = ? AND library_id = ?
		   AND NOT EXISTS (
		       SELECT 1 FROM `+tables.entity+` o
		       WHERE o.library_id = `+tables.entity+`.library_id
		         AND o.normalized_name = ? AND o.id <> `+tables.entity+`.id)`,
		display, normalized, entityID, libraryID, normalized)
	if err != nil {
		return store.CatalogEntity{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return store.CatalogEntity{}, err
	} else if n == 0 {
		// Nothing changed for one of two reasons, and the caller is owed
		// the difference: a name that is taken is an invitation to merge,
		// an id that is not there is a mistake.
		//
		// The check runs on this transaction rather than the pool. A
		// write transaction holds SQLite's write lock, so a second
		// connection asking the same question would wait for a
		// transaction that is waiting for it.
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+tables.entity+`
			 WHERE id = ? AND library_id = ?`, entityID, libraryID).
			Scan(&exists); err != nil {
			return store.CatalogEntity{}, err
		}
		if exists == 0 {
			return store.CatalogEntity{}, store.ErrNotFound
		}
		return store.CatalogEntity{}, store.ErrConflict
	}
	// Every book claiming it is findable by the old spelling until the
	// index is rebuilt, and by the new one only afterwards. Both happen
	// in this transaction, so neither state is ever observable.
	if err := reindexEntityBooksTx(ctx, tx, tables, entityID); err != nil {
		return store.CatalogEntity{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.CatalogEntity{}, err
	}
	return s.CatalogEntityByID(ctx, userID, libraryID, entityID, kind)
}

func (s *Store) MergeCatalogEntities(
	ctx context.Context,
	userID, libraryID, fromID, intoID string,
	kind store.EntityKind,
	at time.Time,
) (int, error) {
	tables, err := tablesFor(kind)
	if err != nil {
		return 0, err
	}
	if fromID == "" || intoID == "" || fromID == intoID {
		return 0, store.ErrInvalidTransition
	}
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleManage); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Both entities are confirmed to exist in this library before
	// anything moves, so merging into an id from another library is a
	// not-found rather than a silent no-op that reports success.
	for _, id := range []string{fromID, intoID} {
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+tables.entity+`
			 WHERE id = ? AND library_id = ?`, id, libraryID).
			Scan(&exists); err != nil {
			return 0, err
		}
		if exists == 0 {
			return 0, store.ErrNotFound
		}
	}

	// A book claiming both entities keeps the row it already had for the
	// target, because the merge is a statement about which entities are
	// the same, not about what any book asserts. Two things do carry
	// over, because dropping them would lose something a person said: a
	// lock, and — for series — a position the target row does not have.
	if err := mergeEntityRows(ctx, tx, tables, kind, libraryID, fromID, intoID); err != nil {
		return 0, err
	}
	moved, err := tx.ExecContext(ctx,
		`UPDATE `+tables.membership+` SET `+tables.column+` = ?
		 WHERE library_id = ? AND `+tables.column+` = ?`,
		intoID, libraryID, fromID)
	if err != nil {
		return 0, err
	}
	count, err := moved.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM `+tables.entity+` WHERE id = ? AND library_id = ?`,
		fromID, libraryID); err != nil {
		return 0, err
	}
	// The books that changed shape are bumped so a concurrent metadata
	// write against a pre-merge snapshot loses on revision instead of
	// resurrecting the entity it just lost.
	if _, err := tx.ExecContext(ctx,
		`UPDATE books SET revision = revision + 1, updated_at = ?
		 WHERE library_id = ? AND id IN (
		     SELECT book_id FROM `+tables.membership+`
		     WHERE library_id = ? AND `+tables.column+` = ?)`,
		formatTime(at), libraryID, libraryID, intoID); err != nil {
		return 0, err
	}
	// Those same books are findable by a name that no longer exists until
	// the index is rebuilt, which is why this is inside the transaction:
	// the catalog and what it can be found by commit together.
	if err := reindexEntityBooksTx(ctx, tx, tables, intoID); err != nil {
		return 0, err
	}
	return int(count), tx.Commit()
}

// mergeEntityRows folds the memberships a book holds on both sides into
// the row it keeps, then deletes the losing rows so the bulk UPDATE that
// follows cannot collide with the target's primary key.
func mergeEntityRows(
	ctx context.Context,
	tx *sql.Tx,
	tables entityTables,
	kind store.EntityKind,
	libraryID, fromID, intoID string,
) error {
	// Contributors are keyed by role as well, so the same person credited
	// as author under one entity and translator under another keeps both
	// rows: they are different claims, not a collision.
	match := ""
	if kind == store.EntityContributor {
		match = " AND f.role = t.role"
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE `+tables.membership+` AS t SET locked = 1
		 WHERE t.library_id = ? AND t.`+tables.column+` = ?
		   AND EXISTS (
		       SELECT 1 FROM `+tables.membership+` f
		       WHERE f.library_id = t.library_id AND f.book_id = t.book_id
		         AND f.`+tables.column+` = ? AND f.locked = 1`+match+`)`,
		libraryID, intoID, fromID); err != nil {
		return err
	}
	if kind == store.EntitySeries {
		if _, err := tx.ExecContext(ctx,
			`UPDATE book_series AS t
			 SET position = (
			     SELECT f.position FROM book_series f
			     WHERE f.library_id = t.library_id AND f.book_id = t.book_id
			       AND f.series_id = ?)
			 WHERE t.library_id = ? AND t.series_id = ? AND t.position IS NULL
			   AND EXISTS (
			       SELECT 1 FROM book_series f
			       WHERE f.library_id = t.library_id AND f.book_id = t.book_id
			         AND f.series_id = ? AND f.position IS NOT NULL)`,
			fromID, libraryID, intoID, fromID); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx,
		`DELETE FROM `+tables.membership+` AS f
		 WHERE f.library_id = ? AND f.`+tables.column+` = ?
		   AND EXISTS (
		       SELECT 1 FROM `+tables.membership+` t
		       WHERE t.library_id = f.library_id AND t.book_id = f.book_id
		         AND t.`+tables.column+` = ?`+match+`)`,
		libraryID, fromID, intoID)
	return err
}
