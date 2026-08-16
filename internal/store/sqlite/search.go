package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// reindexBookTx rebuilds one book's search row from what the book and its
// entities currently say. It is called by every write that changes either,
// inside that write's own transaction, so the index cannot describe a
// state the catalog never had.
//
// Rebuilding rather than patching is deliberate: a book's searchable text
// is spread over seven tables, and a patch would have to know which of
// them each caller touched. Reading it back costs one query on a write
// path that is already several.
func reindexBookTx(ctx context.Context, tx *sql.Tx, bookID string) error {
	var folderID, title, subtitle, description, publisher string
	err := tx.QueryRowContext(ctx,
		`SELECT folder_id, title, COALESCE(subtitle, ''),
		        COALESCE(description, ''), COALESCE(publisher, '')
		 FROM books WHERE id = ?`, bookID).
		Scan(&folderID, &title, &subtitle, &description, &publisher)
	if err != nil {
		// A book that is gone has nothing to index, and its row goes with
		// it. Deleting unconditionally means a delete needs no separate
		// call to keep the index honest.
		if err == sql.ErrNoRows {
			_, err := tx.ExecContext(ctx,
				`DELETE FROM book_search WHERE book_id = ?`, bookID)
			return err
		}
		return err
	}
	people, err := indexedNamesTx(ctx, tx, bookID,
		`SELECT c.name FROM book_contributors m
		 JOIN contributors c ON c.id = m.contributor_id WHERE m.book_id = ?`)
	if err != nil {
		return err
	}
	// Series are searchable alongside tags rather than
	// alongside people: somebody typing "discworld" is naming what the
	// book is about as surely as somebody typing "fantasy".
	subjects, err := indexedNamesTx(ctx, tx, bookID,
		`SELECT t.name FROM book_tags m JOIN tags t ON t.id = m.tag_id
		   WHERE m.book_id = ?
		 UNION ALL
		 SELECT s.name FROM book_series m JOIN series s ON s.id = m.series_id
		   WHERE m.book_id = ?`, bookID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM book_search WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO book_search (
		     book_id, folder_id, title, subtitle, description,
		     publisher, people, subjects)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		bookID, folderID, title, subtitle, description, publisher,
		people, subjects)
	return err
}

// indexedNamesTx joins one book's entity names into a blob of text. The
// separator is a newline because the tokenizer treats it as one, so no
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

// matchExpression turns words into an FTS5 query. Each term is written as
// a quoted string with a prefix wildcard, so a person typing three
// letters of an author's name finds the author, and every term must match
// because narrowing is what somebody adding a word is asking for.
//
// The quoting is what makes this safe: SearchTerms has already reduced
// the input to letters and digits, and the quotes mean even a term that
// somehow held an operator would be read as text.
func matchExpression(terms []string) string {
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"*`)
	}
	return strings.Join(quoted, " AND ")
}

// bm25Weights scores the columns in the order the table declares them.
// The first two are the unindexed keys and can never match. A title match
// beats a series or author match beats a description match, because
// somebody typing two words is far more often naming a book than quoting
// one.
const bm25Weights = `bm25(book_search, 0.0, 0.0, 10.0, 5.0, 1.0, 2.0, 6.0, 4.0)`

func (s *Store) SearchCatalogBooks(
	ctx context.Context, query store.SearchQuery,
) (store.SearchResult, error) {
	if query.Limit < 1 || query.Limit > store.MaxSearchLimit {
		return store.SearchResult{}, fmt.Errorf(
			"%w: search limit %d", store.ErrInvalidInput, query.Limit)
	}
	terms := store.SearchTerms(query.Text)
	scored := len(terms) > 0
	if !scored && len(query.Entities) == 0 {
		// No words and no filter is not an error and not the whole
		// folder: it is a search box nobody has typed in yet.
		return store.SearchResult{}, nil
	}

	// The statement is assembled in one pass so that every placeholder
	// gets its argument as it is written. Nothing here is interpolated
	// from the caller — the only concatenated names come from the closed
	// set in tablesFor.
	var sqlText strings.Builder
	var args []any
	// The series resolution is only pulled in when a filter can name a
	// series, so an ordinary word search costs exactly what it did.
	if len(query.Entities) > 0 {
		sqlText.WriteString(effectiveSeriesCTE)
		args = append(args, effectiveSeriesArgs(query.UserID)...)
	}
	sqlText.WriteString(`SELECT ` + bookColumns)
	if scored {
		sqlText.WriteString(`, ` + bm25Weights)
	}
	sqlText.WriteString(` FROM books b`)
	if scored {
		sqlText.WriteString(`
		 JOIN book_search ON book_search.book_id = b.id
		   AND book_search MATCH ?`)
		args = append(args, matchExpression(terms))
	}
	sqlText.WriteString(`
	 WHERE b.folder_id = ?`)
	args = append(args, query.FolderID)
	// A filter narrows by entity id whatever kind it is, because a caller
	// holding an id from a facet should not have to tell the server what
	// kind of thing it named.
	//
	// The series arm reads the resolved memberships, so filtering by a
	// series facet finds the books the reader was shown in it — a book
	// they claimed into the series, and not one they claimed out of it.
	for _, id := range query.Entities {
		sqlText.WriteString(`
		   AND EXISTS (
		       SELECT 1 FROM book_tags m WHERE m.book_id = b.id AND m.tag_id = ?
		       UNION ALL
		       SELECT 1 FROM eff_series m WHERE m.book_id = b.id AND m.series_id = ?
		       UNION ALL
		       SELECT 1 FROM book_contributors m
		         WHERE m.book_id = b.id AND m.contributor_id = ?)`)
		args = append(args, id, id, id)
	}
	sqlText.WriteString(`
	 ORDER BY `)
	if scored {
		sqlText.WriteString(bm25Weights + `, `)
	}
	// Newest first among equals, matching every other catalog listing.
	sqlText.WriteString(`b.created_at DESC, b.id LIMIT ?`)
	// One row over the limit is asked for so the answer can say it was
	// cut, rather than leaving a caller to guess from a full page.
	args = append(args, query.Limit+1)

	rows, err := s.db.QueryContext(ctx, sqlText.String(), args...)
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
	if result.Facets, err = s.searchFacets(ctx, query.UserID, ids); err != nil {
		return store.SearchResult{}, err
	}
	return result, nil
}

// scanCatalogBookScored reads a book row that carries a relevance score
// on the end. The score is only used for ordering, which the database has
// already done, so it is scanned and dropped rather than reaching the
// caller: a bm25 number means nothing outside the query that produced it,
// and returning one would invite a client to compare across searches.
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
	ctx context.Context, userID string, bookIDs []string,
) ([]store.SearchFacet, error) {
	if len(bookIDs) == 0 {
		return nil, nil
	}
	var out []store.SearchFacet
	for _, kind := range []store.EntityKind{
		store.EntitySeries, store.EntityContributor, store.EntityTag,
	} {
		tables, err := tablesFor(kind)
		if err != nil {
			return nil, err
		}
		prefix, membership, args := tables.membershipFor(kind, userID)
		nameJoin, name, normalized, _, _ := tables.nameColumns(kind)
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(bookIDs)), ",")
		for _, id := range bookIDs {
			args = append(args, id)
		}
		args = append(args, store.MaxSearchFacets)
		rows, err := s.db.QueryContext(ctx,
			prefix+
				`SELECT e.id, `+name+`, COUNT(*) AS n
				 FROM `+membership+` m
				 JOIN `+tables.entity+` e ON e.id = m.`+tables.column+
				nameJoin+`
				 WHERE m.book_id IN (`+placeholders+`)
				 GROUP BY e.id, `+name+`, `+normalized+`
				 ORDER BY n DESC, `+normalized+`
				 LIMIT ?`, args...)
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
