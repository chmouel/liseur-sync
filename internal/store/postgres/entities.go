package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
	default:
		return entityTables{}, fmt.Errorf("%w: entity kind %q",
			store.ErrInvalidInput, kind)
	}
}

// membership is what a query should read a kind's memberships from, for
// one reader. Series go through the override resolution (ADR-0018),
// which is a CTE rather than a table, so it comes with a prefix to put
// in front of the query and the bind parameters that prefix consumes.
// Every other kind is shared and reads its table directly.
func (t entityTables) membershipFor(
	kind store.EntityKind, userID string,
) (prefix, table string, args []any) {
	if kind != store.EntitySeries {
		return "", t.membership, nil
	}
	return effectiveSeriesCTE, "eff_series", effectiveSeriesArgs(userID)
}

// Entity counts are over active books only, so an entity whose books are
// all currently missing reads as empty rather than as a populated entity
// whose page turns out to be blank.
const entityCountExpr = `(SELECT COUNT(*) FROM %s m
	         JOIN books b ON b.id = m.book_id
	         WHERE m.%s = e.id AND b.status = 'active')`

func (s *Store) ListCatalogEntities(
	ctx context.Context, userID string, kind store.EntityKind,
	after string, limit int,
) ([]store.CatalogEntity, error) {
	if limit < 1 || limit > store.MaxEntityListLimit {
		return nil, fmt.Errorf("%w: entity limit %d", store.ErrInvalidInput, limit)
	}
	tables, err := tablesFor(kind)
	if err != nil {
		return nil, err
	}
	prefix, membership, args := tables.membershipFor(kind, userID)
	args = append(args, after, limit)
	rows, err := s.db.QueryContext(ctx, q(
		prefix+
			`SELECT e.id, e.name, e.normalized_name, e.created_at,
			        `+fmt.Sprintf(entityCountExpr, membership, tables.column)+`
			 FROM `+tables.entity+` e
			 WHERE e.normalized_name > ?
			 ORDER BY e.normalized_name LIMIT ?`),
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.CatalogEntity{}
	for rows.Next() {
		entity := store.CatalogEntity{Kind: kind}
		if err := rows.Scan(&entity.ID, &entity.Name, &entity.NormalizedName,
			&entity.CreatedAt, &entity.BookCount); err != nil {
			return nil, err
		}
		entity.CreatedAt = entity.CreatedAt.UTC()
		out = append(out, entity)
	}
	return out, rows.Err()
}

func (s *Store) CatalogEntityByID(
	ctx context.Context, userID, entityID string, kind store.EntityKind,
) (store.CatalogEntity, error) {
	tables, err := tablesFor(kind)
	if err != nil {
		return store.CatalogEntity{}, err
	}
	prefix, membership, args := tables.membershipFor(kind, userID)
	args = append(args, entityID)
	entity := store.CatalogEntity{Kind: kind}
	err = s.db.QueryRowContext(ctx, q(
		prefix+
			`SELECT e.id, e.name, e.normalized_name, e.created_at,
			        `+fmt.Sprintf(entityCountExpr, membership, tables.column)+`
			 FROM `+tables.entity+` e
			 WHERE e.id = ?`),
		args...).
		Scan(&entity.ID, &entity.Name, &entity.NormalizedName,
			&entity.CreatedAt, &entity.BookCount)
	if errors.Is(err, sql.ErrNoRows) {
		return store.CatalogEntity{}, store.ErrNotFound
	}
	if err != nil {
		return store.CatalogEntity{}, err
	}
	entity.CreatedAt = entity.CreatedAt.UTC()
	return entity, nil
}

func (s *Store) ListBooksByEntity(
	ctx context.Context, userID, entityID string, kind store.EntityKind,
	after *store.CatalogBookCursor, limit int,
) ([]store.CatalogBook, *store.CatalogBookCursor, error) {
	if limit < 1 || limit > 500 {
		return nil, nil, fmt.Errorf("%w: book limit %d", store.ErrInvalidInput, limit)
	}
	tables, err := tablesFor(kind)
	if err != nil {
		return nil, nil, err
	}
	// A series is the one kind with an order of its own, and it is the
	// order a reader wants: book three of a trilogy is not interesting
	// for having been scanned first. Books with no position sort last,
	// because an unplaced book is an unanswered question rather than
	// book zero — which is what the coalesced sentinel buys, at the
	// price of never allowing a real position that large.
	//
	// The cursor compares against the same expression it is ordered by.
	// Anything less would page incoherently, because one reconciliation
	// pass stamps a whole folder with a single created_at. That
	// expression now reads a resolved position rather than a scanned
	// one, so an overridden series pages in the order it is shown, and
	// a shelf drawing on several folders (ADR-0019) breaks the resulting
	// ties on the book id.
	prefix, membership, args := tables.membershipFor(kind, userID)
	series := kind == store.EntitySeries
	// Only a series membership carries a position; a tag has none at
	// all, so the sort key for every other kind is the sentinel and the
	// ordering falls back to the scan order below.
	sortKey := unplacedSeriesPosition
	if series {
		sortKey = "COALESCE(m.position, " + unplacedSeriesPosition + ")"
	}
	order := "b.created_at, b.id"
	cursor := ""
	args = append(args, entityID)
	if series {
		order = sortKey + ", b.created_at, b.id"
		if after != nil {
			cursor = " AND (" + sortKey + ", b.created_at, b.id) > (?, ?, ?)"
			args = append(args, seriesCursorKey(after),
				after.CreatedAt.UTC(), after.ID)
		}
	} else if after != nil {
		cursor = " AND (b.created_at, b.id) > (?, ?)"
		args = append(args, after.CreatedAt.UTC(), after.ID)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q(
		prefix+
			`SELECT `+bookColumns+`, `+sortKey+`
			 FROM books b
			 JOIN `+membership+` m ON m.book_id = b.id
			 WHERE m.`+tables.column+` = ?
			   AND b.status = 'active'`+
			cursor+` ORDER BY `+order+` LIMIT ?`), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []store.CatalogBook{}
	var lastKey float64
	for rows.Next() {
		book, key, err := scanCatalogBookWithKey(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, book)
		lastKey = key
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(out) < limit {
		return out, nil, nil
	}
	last := out[len(out)-1]
	next := &store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	if series && lastKey != unplacedSeriesSentinel {
		pos := lastKey
		next.SeriesPosition = &pos
	}
	return out, next, nil
}

// unplacedSeriesPosition sorts a book with no place in its series after
// every book that has one. It is a literal rather than a NULL because
// the pagination cursor has to compare against the same expression the
// rows are ordered by, and a NULL in a row-value comparison answers
// nothing.
const (
	unplacedSeriesPosition = "1e308"
	unplacedSeriesSentinel = 1e308
)

// seriesCursorKey turns a cursor's optional position back into the sort
// key it was read from: absent means the book had no position, which is
// the sentinel.
func seriesCursorKey(c *store.CatalogBookCursor) float64 {
	if c.SeriesPosition == nil {
		return unplacedSeriesSentinel
	}
	return *c.SeriesPosition
}

// scanCatalogBookWithKey reads a catalog book plus the trailing sort-key
// column the entity listing appends, without giving every other book
// query a column it does not want.
func scanCatalogBookWithKey(
	row interface{ Scan(...any) error },
) (store.CatalogBook, float64, error) {
	var key float64
	book, err := scanCatalogBook(trailingScan{row: row, extra: &key})
	return book, key, err
}
