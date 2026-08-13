package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// reindexBookTx rebuilds one book's search vector from what the book and
// its entities currently say. It is called by every write that changes
// either, inside that write's own transaction, so the index cannot
// describe a state the catalog never had.
//
// The text is folded in Go rather than by `unaccent`, which is an
// extension a managed database may not offer, and the vector is built
// with the `simple` configuration rather than `english`, which stems.
// Both choices exist so that this backend answers the same question as
// the SQLite one.
func reindexBookTx(ctx context.Context, tx *sql.Tx, bookID string) error {
	var title, subtitle, description, publisher string
	err := tx.QueryRowContext(ctx, q(
		`SELECT title, COALESCE(subtitle, ''), COALESCE(description, ''),
		        COALESCE(publisher, '')
		 FROM books WHERE id = ?`), bookID).
		Scan(&title, &subtitle, &description, &publisher)
	if errors.Is(err, sql.ErrNoRows) {
		// A book that is gone has nothing to index and no row to hold it.
		return nil
	}
	if err != nil {
		return err
	}
	people, err := indexedNamesTx(ctx, tx, bookID, q(
		`SELECT c.name FROM book_contributors m
		 JOIN contributors c ON c.id = m.contributor_id WHERE m.book_id = ?`))
	if err != nil {
		return err
	}
	// Series sit with tags and genres rather than with people: somebody
	// typing "discworld" is naming what the book is about as surely as
	// somebody typing "fantasy".
	subjects, err := indexedNamesTx(ctx, tx, bookID, q(
		`SELECT t.name FROM book_tags m JOIN tags t ON t.id = m.tag_id
		   WHERE m.book_id = ?
		 UNION ALL
		 SELECT g.name FROM book_genres m JOIN genres g ON g.id = m.genre_id
		   WHERE m.book_id = ?
		 UNION ALL
		 SELECT s.name FROM book_series m JOIN series s ON s.id = m.series_id
		   WHERE m.book_id = ?`), bookID, bookID)
	if err != nil {
		return err
	}
	// The weights are the same ranking the SQLite bm25 weights express:
	// a title match beats a series or author match beats a description
	// match, because somebody typing two words is far more often naming
	// a book than quoting one.
	_, err = tx.ExecContext(ctx, q(
		`UPDATE books SET search_vector =
		     setweight(to_tsvector('simple', ?), 'A') ||
		     setweight(to_tsvector('simple', ?), 'B') ||
		     setweight(to_tsvector('simple', ?), 'C') ||
		     setweight(to_tsvector('simple', ?), 'D')
		 WHERE id = ?`),
		store.FoldForSearch(title),
		store.FoldForSearch(people+"\n"+subjects),
		store.FoldForSearch(subtitle+"\n"+publisher),
		store.FoldForSearch(description),
		bookID)
	return err
}

// indexedNamesTx joins one book's entity names into a blob of text. The
// separator is a newline because every tokenizer treats it as one, so no
// query can match across two names by accident.
func indexedNamesTx(
	ctx context.Context, tx *sql.Tx, bookID, query string, extra ...any,
) (string, error) {
	args := append([]any{bookID}, extra...)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", err
		}
		names = append(names, name)
	}
	return strings.Join(names, "\n"), rows.Err()
}

// reindexEntityBooksTx reindexes every book claiming one entity, which is
// what a rename or a merge changes: the book did not move, but what it is
// findable by did.
func reindexEntityBooksTx(
	ctx context.Context, tx *sql.Tx, tables entityTables, entityID string,
) error {
	rows, err := tx.QueryContext(ctx, q(
		`SELECT book_id FROM `+tables.membership+` WHERE `+tables.column+` = ?`),
		entityID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := reindexBookTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

// matchExpression turns words into a tsquery. Each term gets a prefix
// wildcard, so a person typing three letters of an author's name finds
// the author, and every term must match because narrowing is what
// somebody adding a word is asking for.
//
// SearchTerms has already reduced the input to letters and digits, so
// nothing here can be read as tsquery syntax.
func matchExpression(terms []string) string {
	prefixed := make([]string, 0, len(terms))
	for _, term := range terms {
		prefixed = append(prefixed, term+":*")
	}
	return strings.Join(prefixed, " & ")
}

func (s *Store) SearchCatalogBooks(
	ctx context.Context, userID string, query store.SearchQuery,
) (store.SearchResult, error) {
	if query.Limit < 1 || query.Limit > store.MaxSearchLimit {
		return store.SearchResult{}, store.ErrInvalidTransition
	}
	if _, err := s.LibraryByID(
		ctx, userID, query.LibraryID, store.LibraryRoleRead); err != nil {
		return store.SearchResult{}, err
	}
	terms := store.SearchTerms(query.Text)
	scored := len(terms) > 0
	if !scored && len(query.Entities) == 0 {
		// No words and no filter is not an error and not the whole
		// library: it is a search box nobody has typed in yet.
		return store.SearchResult{}, nil
	}

	const rank = `ts_rank('{0.2, 0.4, 0.7, 1.0}'::float4[], b.search_vector,
	                      to_tsquery('simple', ?))`
	var sqlText strings.Builder
	var args []any
	sqlText.WriteString(`SELECT ` + bookColumns)
	if scored {
		sqlText.WriteString(`, ` + rank)
		args = append(args, matchExpression(terms))
	}
	sqlText.WriteString(`
	 FROM books b
	 JOIN libraries l ON l.id = b.library_id
	 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
	 WHERE b.status <> 'trashed' AND b.library_id = ?`)
	args = append(args, userID, query.LibraryID)
	if scored {
		sqlText.WriteString(`
		   AND b.search_vector @@ to_tsquery('simple', ?)`)
		args = append(args, matchExpression(terms))
	}
	// A filter narrows by entity id whatever kind it is, because a caller
	// holding an id from a facet should not have to tell the server what
	// kind of thing it named.
	for _, id := range query.Entities {
		sqlText.WriteString(`
		   AND EXISTS (
		       SELECT 1 FROM book_tags m WHERE m.book_id = b.id AND m.tag_id = ?
		       UNION ALL
		       SELECT 1 FROM book_genres m WHERE m.book_id = b.id AND m.genre_id = ?
		       UNION ALL
		       SELECT 1 FROM book_series m WHERE m.book_id = b.id AND m.series_id = ?
		       UNION ALL
		       SELECT 1 FROM book_contributors m
		         WHERE m.book_id = b.id AND m.contributor_id = ?)`)
		args = append(args, id, id, id, id)
	}
	// The ACL is repeated inside the query, as in every other catalog
	// read here, so it stays safe if a future caller forgets the gate.
	sqlText.WriteString(`
	   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))
	 ORDER BY `)
	args = append(args, userID)
	if scored {
		sqlText.WriteString(rank + ` DESC, `)
		args = append(args, matchExpression(terms))
	}
	// Newest first among equals, matching every other catalog listing.
	sqlText.WriteString(`b.created_at DESC, b.id LIMIT ?`)
	// One row over the limit is asked for so the answer can say it was
	// cut, rather than leaving a caller to guess from a full page.
	args = append(args, query.Limit+1)

	rows, err := s.db.QueryContext(ctx, q(sqlText.String()), args...)
	if err != nil {
		return store.SearchResult{}, err
	}
	defer rows.Close()
	var result store.SearchResult
	var ids []string
	for rows.Next() {
		var book store.CatalogBook
		var score float64
		if scored {
			book, err = scanCatalogBookScored(rows, &score)
		} else {
			book, err = scanCatalogBook(rows)
		}
		if err != nil {
			return store.SearchResult{}, err
		}
		result.Books = append(result.Books, book)
		ids = append(ids, book.ID)
	}
	if err := rows.Err(); err != nil {
		return store.SearchResult{}, err
	}
	if len(result.Books) > query.Limit {
		result.Books = result.Books[:query.Limit]
		ids = ids[:query.Limit]
		result.Truncated = true
	}
	if result.Facets, err = s.searchFacets(ctx, ids); err != nil {
		return store.SearchResult{}, err
	}
	return result, nil
}

// scanCatalogBookScored reads a book row that carries a relevance score on
// the end. The score is only used for ordering, which the database has
// already done, so it is scanned and dropped rather than reaching the
// caller: a rank means nothing outside the query that produced it, and
// returning one would invite a client to compare across searches.
func scanCatalogBookScored(
	row interface{ Scan(...any) error }, score *float64,
) (store.CatalogBook, error) {
	return scanCatalogBook(trailingScan{row: row, extra: score})
}

type trailingScan struct {
	row   interface{ Scan(...any) error }
	extra any
}

func (t trailingScan) Scan(dest ...any) error {
	return t.row.Scan(append(dest, t.extra)...)
}

// searchFacets counts what the matched books have in common. It reads the
// matched ids rather than re-running the search, so the counts can never
// describe a different set of books than the one that was returned.
func (s *Store) searchFacets(
	ctx context.Context, bookIDs []string,
) ([]store.SearchFacet, error) {
	if len(bookIDs) == 0 {
		return nil, nil
	}
	var out []store.SearchFacet
	for _, kind := range []store.EntityKind{
		store.EntitySeries, store.EntityContributor,
		store.EntityTag, store.EntityGenre,
	} {
		tables, err := tablesFor(kind)
		if err != nil {
			return nil, err
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(bookIDs)), ",")
		args := make([]any, 0, len(bookIDs)+1)
		for _, id := range bookIDs {
			args = append(args, id)
		}
		args = append(args, store.MaxSearchFacets)
		rows, err := s.db.QueryContext(ctx, q(
			`SELECT e.id, e.name, COUNT(*) AS n
			 FROM `+tables.membership+` m
			 JOIN `+tables.entity+` e ON e.id = m.`+tables.column+`
			 WHERE m.book_id IN (`+placeholders+`)
			 GROUP BY e.id, e.name, e.normalized_name
			 ORDER BY n DESC, e.normalized_name
			 LIMIT ?`), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			facet := store.SearchFacet{Kind: kind}
			if err := rows.Scan(&facet.ID, &facet.Name, &facet.BookCount); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, facet)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
