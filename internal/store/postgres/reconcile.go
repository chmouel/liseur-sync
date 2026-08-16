package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) BooksInFolder(ctx context.Context, folderID string) ([]store.KnownBook, error) {
	rows, err := s.db.QueryContext(ctx, q(
		`SELECT id, status, relative_path, size_bytes, mtime,
		        content_sha256, calibre_id, cover_sha256
		 FROM books WHERE folder_id = ? ORDER BY relative_path`), folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := []store.KnownBook{}
	for rows.Next() {
		var (
			b         store.KnownBook
			status    string
			calibreID sql.NullInt64
			coverSHA  sql.NullString
		)
		if err := rows.Scan(&b.ID, &status, &b.RelativePath, &b.SizeBytes,
			&b.MTime, &b.ContentSHA256, &calibreID, &coverSHA); err != nil {
			return nil, err
		}
		b.Status = store.BookStatus(status)
		if calibreID.Valid {
			id := calibreID.Int64
			b.CalibreID = &id
		}
		b.CoverSHA256 = coverSHA.String
		b.MTime = b.MTime.UTC()
		known = append(known, b)
	}
	return known, rows.Err()
}

// ReconcileFolder writes one pass's findings in one transaction.
//
// The two guards below are the reason `complete` is a parameter rather
// than a comment: a caller that got them wrong would hide a whole
// catalog, and this is the one place that can refuse.
func (s *Store) ReconcileFolder(
	ctx context.Context, folderID string, observed []store.ObservedBook,
	complete bool, at time.Time,
) (store.ReconcileResult, error) {
	var result store.ReconcileResult
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var kind string
		if err := tx.QueryRowContext(ctx,
			q(`SELECT kind FROM folders WHERE id = ?`), folderID).Scan(&kind); err != nil {
			if err == sql.ErrNoRows {
				return store.ErrNotFound
			}
			return err
		}
		byCalibreID := store.FolderKind(kind) == store.FolderCalibre

		existing, err := existingBooksTx(ctx, tx, folderID, byCalibreID)
		if err != nil {
			return err
		}

		// A Calibre folder identifies books by calibre_id, so a pass can
		// legitimately want to give book A the path book B currently
		// holds — Calibre renames directories on a title edit, and two
		// books can swap in one edit session. Parking every path on an
		// unreachable value first means the unique index never sees the
		// intermediate state. A relative path can never begin with a
		// newline, so nothing real collides with the parked value.
		if byCalibreID && len(observed) > 0 {
			if _, err := tx.ExecContext(ctx,
				q(`UPDATE books SET relative_path = chr(10) || id
				 WHERE folder_id = ?`), folderID); err != nil {
				return err
			}
		}

		seen := map[string]bool{}
		for _, obs := range observed {
			key, err := observationKey(obs, byCalibreID)
			if err != nil {
				return err
			}
			prior, had := existing[key]

			// Rule 4: content change is not identity transfer. The old
			// row goes, taking its identifiers, relations and the
			// user_book_works mapping of everyone who was reading it,
			// because whatever was copied over that path is not the
			// book they were reading.
			if had && obs.Replaces {
				if err := deleteBookTx(ctx, tx, folderID, prior); err != nil {
					return err
				}
				had = false
				result.Replaced++
			}

			var bookID string
			switch {
			case had && obs.Unchanged:
				// The pass recognised this file by its stat and did not
				// re-read it, so it carries no metadata to write. Only
				// the stat and the status are refreshed; overwriting the
				// title with the nothing in hand would empty the catalog
				// on the second pass.
				bookID = prior
				returned, err := touchBookTx(ctx, tx, folderID, prior, obs, at)
				if err != nil {
					return err
				}
				if returned {
					result.Returned++
				}
				seen[key] = true
				continue
			case had:
				bookID = prior
				returned, err := updateBookTx(ctx, tx, folderID, prior, obs, at)
				if err != nil {
					return err
				}
				if returned {
					result.Returned++
				}
				result.Updated++
			default:
				bookID = store.NewID()
				if err := insertBookTx(ctx, tx, folderID, bookID, obs, at); err != nil {
					return err
				}
				if !obs.Replaces {
					result.Added++
				}
			}

			if err := replaceRelationsTx(ctx, tx, folderID, bookID, obs, at); err != nil {
				return err
			}
			if err := reindexBookTx(ctx, tx, bookID); err != nil {
				return err
			}
			seen[key] = true
		}

		// Rules 1 and 2. A pass that hit a read error, a parse failure
		// or a scan bound does not know what it did not see; a pass that
		// observed nothing is indistinguishable from an unmounted mount
		// point, which is still readable and still empty. In both cases
		// the honest answer is to record what was seen and conclude
		// nothing about what was not.
		if !complete || len(observed) == 0 {
			return nil
		}
		missing, err := markMissingTx(ctx, tx, folderID, seen, byCalibreID, at)
		if err != nil {
			return err
		}
		result.Missing = missing
		return nil
	})
	return result, err
}

// observationKey picks the identity key the folder's kind dictates.
func observationKey(obs store.ObservedBook, byCalibreID bool) (string, error) {
	if byCalibreID {
		if obs.CalibreID == nil {
			return "", fmt.Errorf("%w: calibre folder observation without calibre id",
				store.ErrInvalidInput)
		}
		return fmt.Sprintf("c:%d", *obs.CalibreID), nil
	}
	if obs.RelativePath == "" {
		return "", fmt.Errorf("%w: observation without relative path", store.ErrInvalidInput)
	}
	return "p:" + obs.RelativePath, nil
}

func existingBooksTx(
	ctx context.Context, tx *sql.Tx, folderID string, byCalibreID bool,
) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, q(
		`SELECT id, relative_path, calibre_id FROM books WHERE folder_id = ?`), folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var (
			id, path  string
			calibreID sql.NullInt64
		)
		if err := rows.Scan(&id, &path, &calibreID); err != nil {
			return nil, err
		}
		switch {
		case byCalibreID && calibreID.Valid:
			out[fmt.Sprintf("c:%d", calibreID.Int64)] = id
		case !byCalibreID:
			out["p:"+path] = id
		}
	}
	return out, rows.Err()
}

func deleteBookTx(ctx context.Context, tx *sql.Tx, folderID, bookID string) error {
	_, err := tx.ExecContext(ctx,
		q(`DELETE FROM books WHERE id = ? AND folder_id = ?`), bookID, folderID)
	return err
}

func insertBookTx(
	ctx context.Context, tx *sql.Tx, folderID, bookID string,
	obs store.ObservedBook, at time.Time,
) error {
	_, err := tx.ExecContext(ctx, q(
		`INSERT INTO books (
			id, folder_id, status,
			relative_path, size_bytes, mtime, content_sha256,
			original_filename, media_type, calibre_id,
			cover_relative_path, cover_sha256,
			title, subtitle, description, publisher, published_date,
			created_at, updated_at, seen_at, absent_at)
		 VALUES (?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`),
		bookID, folderID,
		obs.RelativePath, obs.SizeBytes, obs.MTime.UTC(), obs.ContentSHA256,
		obs.OriginalFilename, mediaTypeOf(obs), nullInt64(obs.CalibreID),
		nullStr(obs.CoverRelativePath), obs.CoverSHA256,
		obs.Title, obs.Subtitle, obs.Description, obs.Publisher, obs.PublishedDate,
		at.UTC(), at.UTC(), at.UTC())
	return err
}

// updateBookTx refreshes a book that is still the same book. It reports
// whether the book had been marked missing, because a file coming back
// is worth a line in the log.
func updateBookTx(
	ctx context.Context, tx *sql.Tx, folderID, bookID string,
	obs store.ObservedBook, at time.Time,
) (bool, error) {
	var status string
	if err := tx.QueryRowContext(ctx, q(
		`SELECT status FROM books WHERE id = ? AND folder_id = ?`),
		bookID, folderID).Scan(&status); err != nil {
		return false, err
	}
	_, err := tx.ExecContext(ctx, q(
		`UPDATE books SET
			status = 'active',
			relative_path = ?, size_bytes = ?, mtime = ?, content_sha256 = ?,
			original_filename = ?, media_type = ?, calibre_id = ?,
			cover_relative_path = ?, cover_sha256 = ?,
			title = ?, subtitle = ?, description = ?, publisher = ?,
			published_date = ?, updated_at = ?, seen_at = ?, absent_at = NULL
		 WHERE id = ? AND folder_id = ?`),
		obs.RelativePath, obs.SizeBytes, obs.MTime.UTC(), obs.ContentSHA256,
		obs.OriginalFilename, mediaTypeOf(obs), nullInt64(obs.CalibreID),
		nullStr(obs.CoverRelativePath), obs.CoverSHA256,
		obs.Title, obs.Subtitle, obs.Description, obs.Publisher, obs.PublishedDate,
		at.UTC(), at.UTC(),
		bookID, folderID)
	return store.BookStatus(status) == store.BookMissing, err
}

func markMissingTx(
	ctx context.Context, tx *sql.Tx, folderID string,
	seen map[string]bool, byCalibreID bool, at time.Time,
) (int, error) {
	existing, err := existingBooksTx(ctx, tx, folderID, byCalibreID)
	if err != nil {
		return 0, err
	}
	count := 0
	for key, bookID := range existing {
		if seen[key] {
			continue
		}
		res, err := tx.ExecContext(ctx, q(
			`UPDATE books SET status = 'missing', absent_at = ?, updated_at = ?
			 WHERE id = ? AND folder_id = ? AND status = 'active'`),
			at.UTC(), at.UTC(), bookID, folderID)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		count += int(n)
	}
	return count, nil
}

// replaceRelationsTx rewrites a book's metadata sets wholesale. With no
// manual editing and no external providers there is one source per
// folder and nothing to merge, so "what the folder says now" simply
// replaces "what it said last time".
func replaceRelationsTx(
	ctx context.Context, tx *sql.Tx, folderID, bookID string,
	obs store.ObservedBook, at time.Time,
) error {
	for _, table := range []string{
		"book_identifiers", "book_languages", "book_tags",
		"book_series", "book_contributors",
	} {
		if _, err := tx.ExecContext(ctx, q(
			`DELETE FROM `+table+` WHERE folder_id = ? AND book_id = ?`),
			folderID, bookID); err != nil {
			return err
		}
	}

	for _, id := range obs.Identifiers {
		if id.Scheme == "" || id.Value == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO book_identifiers (folder_id, book_id, scheme, value)
			 VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`),
			folderID, bookID, id.Scheme, id.Value); err != nil {
			return err
		}
	}
	for _, lang := range obs.Languages {
		if lang == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO book_languages (folder_id, book_id, language)
			 VALUES (?, ?, ?) ON CONFLICT DO NOTHING`),
			folderID, bookID, lang); err != nil {
			return err
		}
	}
	for _, tag := range obs.Tags {
		tagID, err := resolveEntityTx(ctx, tx, "tags", folderID, tag, at)
		if err != nil || tagID == "" {
			if err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO book_tags (folder_id, book_id, tag_id)
			 VALUES (?, ?, ?) ON CONFLICT DO NOTHING`),
			folderID, bookID, tagID); err != nil {
			return err
		}
	}
	for _, sr := range obs.Series {
		seriesID, err := resolveEntityTx(ctx, tx, "series", folderID, sr.Name, at)
		if err != nil || seriesID == "" {
			if err != nil {
				return err
			}
			continue
		}
		var position any
		if sr.Position != nil {
			position = *sr.Position
		}
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO book_series (folder_id, book_id, series_id, position)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (book_id, series_id) DO UPDATE SET position = excluded.position`),
			folderID, bookID, seriesID, position); err != nil {
			return err
		}
	}
	for _, c := range obs.Contributors {
		contributorID, err := resolveEntityTx(ctx, tx, "contributors", folderID, c.Name, at)
		if err != nil || contributorID == "" {
			if err != nil {
				return err
			}
			continue
		}
		role := c.Role
		if role == "" {
			role = store.ContributorRoleAuthor
		}
		if _, err := tx.ExecContext(ctx, q(
			`INSERT INTO book_contributors
			     (folder_id, book_id, contributor_id, role, position)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (book_id, contributor_id, role)
			 DO UPDATE SET position = excluded.position`),
			folderID, bookID, contributorID, role, c.Position); err != nil {
			return err
		}
	}
	return nil
}

// resolveEntityTx finds or creates one folder-wide entity by its
// normalized name, keeping the first spelling seen as the display value.
// An empty or whitespace-only name resolves to nothing rather than to an
// entity nobody can name.
func resolveEntityTx(
	ctx context.Context, tx *sql.Tx, table, folderID, name string, at time.Time,
) (string, error) {
	normalized := metadata.NormalizeName(name)
	if normalized == "" {
		return "", nil
	}
	var id string
	err := tx.QueryRowContext(ctx, q(
		`SELECT id FROM `+table+` WHERE folder_id = ? AND normalized_name = ?`),
		folderID, normalized).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	id = store.NewID()
	if _, err := tx.ExecContext(ctx, q(
		`INSERT INTO `+table+` (id, folder_id, name, normalized_name, created_at)
		 VALUES (?, ?, ?, ?, ?)`),
		id, folderID, name, normalized, at.UTC()); err != nil {
		return "", err
	}
	return id, nil
}

func mediaTypeOf(obs store.ObservedBook) string {
	if obs.MediaType == "" {
		return "application/epub+zip"
	}
	return obs.MediaType
}

func nullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

// touchBookTx records that an unchanged file was seen. It writes the
// stat and the status and nothing else, because the pass that produced
// this observation deliberately did not read the file.
func touchBookTx(
	ctx context.Context, tx *sql.Tx, folderID, bookID string,
	obs store.ObservedBook, at time.Time,
) (bool, error) {
	var status string
	if err := tx.QueryRowContext(ctx, q(
		`SELECT status FROM books WHERE id = ? AND folder_id = ?`),
		bookID, folderID).Scan(&status); err != nil {
		return false, err
	}
	_, err := tx.ExecContext(ctx, q(
		`UPDATE books SET status = 'active', relative_path = ?, size_bytes = ?,
		        mtime = ?, seen_at = ?, absent_at = NULL
		 WHERE id = ? AND folder_id = ?`),
		obs.RelativePath, obs.SizeBytes, obs.MTime.UTC(), at.UTC(),
		bookID, folderID)
	return store.BookStatus(status) == store.BookMissing, err
}
