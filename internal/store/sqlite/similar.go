package sqlite

import (
	"context"
	"database/sql"

	"github.com/chmouel/liseur-sync/internal/store"
)

// ListSimilarBooks gathers the facts the similarity rule is allowed to
// see and hands them to store.GroupSimilarBooks. The rule lives there
// rather than in SQL so both backends answer with the same groups; this
// side is only ever about fetching.
func (s *Store) ListSimilarBooks(
	ctx context.Context,
	userID, libraryID string,
	limit int,
) ([]store.SimilarBookGroup, error) {
	if limit < 1 || limit > 500 {
		return nil, store.ErrInvalidTransition
	}
	if _, err := s.LibraryByID(ctx, userID, libraryID, store.LibraryRoleRead); err != nil {
		return nil, err
	}
	// Only active books, for the same reason the digest report uses only
	// active books: a trashed book is one the librarian already resolved.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bookColumns+`
		 FROM books b
		 JOIN libraries l ON l.id = b.library_id
		 LEFT JOIN library_access a ON a.library_id = l.id AND a.user_id = ?
		 WHERE l.id = ? AND b.status = 'active'
		   AND (l.owner_user_id = ? OR a.role IN ('read', 'manage'))
		 ORDER BY b.created_at, b.id LIMIT ?`,
		userID, libraryID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []store.SimilarityCandidate
	index := map[string]int{}
	for rows.Next() {
		book, err := scanCatalogBook(rows)
		if err != nil {
			return nil, err
		}
		index[book.ID] = len(candidates)
		candidates = append(candidates, store.SimilarityCandidate{
			Book:            book,
			SeriesPositions: map[string]float64{},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) < 2 {
		return nil, nil
	}

	// The three follow-up queries are scoped by library rather than by
	// the ids just read, because the library is already the ACL boundary
	// and a list of ids long enough to matter is a statement too long to
	// plan well.
	if err := s.collectSimilarityFacts(ctx, libraryID, index, candidates); err != nil {
		return nil, err
	}
	return store.GroupSimilarBooks(candidates), nil
}

func (s *Store) collectSimilarityFacts(
	ctx context.Context,
	libraryID string,
	index map[string]int,
	candidates []store.SimilarityCandidate,
) error {
	scanPairs := func(query string, apply func(int, string, sql.NullFloat64)) error {
		rows, err := s.db.QueryContext(ctx, query, libraryID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var bookID, value string
			var position sql.NullFloat64
			if err := rows.Scan(&bookID, &value, &position); err != nil {
				return err
			}
			if i, ok := index[bookID]; ok {
				apply(i, value, position)
			}
		}
		return rows.Err()
	}

	if err := scanPairs(
		`SELECT book_id, content_sha256, NULL FROM book_files
		 WHERE library_id = ?`,
		func(i int, digest string, _ sql.NullFloat64) {
			candidates[i].Digests = append(candidates[i].Digests, digest)
		}); err != nil {
		return err
	}
	if err := scanPairs(
		`SELECT book_id, contributor_id, NULL FROM book_contributors
		 WHERE library_id = ?`,
		func(i int, contributorID string, _ sql.NullFloat64) {
			candidates[i].ContributorIDs = append(
				candidates[i].ContributorIDs, contributorID)
		}); err != nil {
		return err
	}
	return scanPairs(
		`SELECT book_id, series_id, position FROM book_series
		 WHERE library_id = ?`,
		func(i int, seriesID string, position sql.NullFloat64) {
			// Only a recorded position counts. An absent one is an
			// unanswered question, and treating it as zero would call
			// every unplaced book volume nought.
			if position.Valid {
				candidates[i].SeriesPositions[seriesID] = position.Float64
			}
		})
}
