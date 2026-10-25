package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

const catalogAudiobookColumns = `ca.id, ca.folder_id, ca.status,
	ca.relative_path, ca.calibre_id, ca.size_bytes, ca.mtime, ca.content_sha256,
	ca.cue_mtime, ca.cue_size_bytes,
	ca.title, ca.authors_json, ca.narrators_json, ca.series_name, ca.asin, ca.isbn,
	ca.duration_seconds, ca.chapters_json,
	ca.created_at, ca.updated_at, ca.seen_at, ca.absent_at`

func scanCatalogAudiobook(row interface{ Scan(...any) error }) (store.CatalogAudiobook, error) {
	var (
		a                store.CatalogAudiobook
		status           string
		calibreID        sql.NullInt64
		mtime            string
		cueMTime         sql.NullString
		authors, narrs   string
		chapters         string
		createdAt, upAt  string
		seenAt, absentAt sql.NullString
	)
	if err := row.Scan(
		&a.ID, &a.FolderID, &status,
		&a.RelativePath, &calibreID, &a.SizeBytes, &mtime, &a.ContentSHA256,
		&cueMTime, &a.CueSizeBytes,
		&a.Title, &authors, &narrs, &a.SeriesName, &a.ASIN, &a.ISBN,
		&a.DurationSeconds, &chapters,
		&createdAt, &upAt, &seenAt, &absentAt,
	); err != nil {
		return store.CatalogAudiobook{}, err
	}
	a.Status = store.BookStatus(status)
	if calibreID.Valid {
		id := calibreID.Int64
		a.CalibreID = &id
	}
	a.AuthorsJSON = []byte(authors)
	a.NarratorsJSON = []byte(narrs)
	a.ChaptersJSON = []byte(chapters)

	var err error
	if a.MTime, err = parseTime(mtime); err != nil {
		return store.CatalogAudiobook{}, err
	}
	if a.CueMTime, err = parseTimePtr(cueMTime); err != nil {
		return store.CatalogAudiobook{}, err
	}
	if a.CreatedAt, err = parseTime(createdAt); err != nil {
		return store.CatalogAudiobook{}, err
	}
	if a.UpdatedAt, err = parseTime(upAt); err != nil {
		return store.CatalogAudiobook{}, err
	}
	if a.SeenAt, err = parseTimePtr(seenAt); err != nil {
		return store.CatalogAudiobook{}, err
	}
	if a.AbsentAt, err = parseTimePtr(absentAt); err != nil {
		return store.CatalogAudiobook{}, err
	}
	return a, nil
}

func (s *Store) AudiobooksInFolder(ctx context.Context, folderID string) ([]store.KnownAudiobook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, status, relative_path, calibre_id, size_bytes, mtime,
		        content_sha256, cue_mtime, cue_size_bytes
		   FROM catalog_audiobooks WHERE folder_id = ? ORDER BY relative_path`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.KnownAudiobook{}
	for rows.Next() {
		var (
			k         store.KnownAudiobook
			status    string
			calibreID sql.NullInt64
			mtime     string
			cueMTime  sql.NullString
		)
		if err := rows.Scan(&k.ID, &status, &k.RelativePath, &calibreID,
			&k.SizeBytes, &mtime, &k.ContentSHA256, &cueMTime, &k.CueSizeBytes); err != nil {
			return nil, err
		}
		k.Status = store.BookStatus(status)
		if calibreID.Valid {
			id := calibreID.Int64
			k.CalibreID = &id
		}
		var err error
		if k.MTime, err = parseTime(mtime); err != nil {
			return nil, err
		}
		if k.CueMTime, err = parseTimePtr(cueMTime); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

type existingAudiobook struct {
	id           string
	status       store.BookStatus
	relativePath string
	calibreID    *int64
	sizeBytes    int64
	mtime        time.Time
	sha256       string
	cueMTime     *time.Time
	cueSizeBytes int64
	title        string
	authorsJSON  string
	narrsJSON    string
	seriesName   string
	asin         string
	isbn         string
	duration     float64
	chaptersJSON string
}

func (e existingAudiobook) differsFrom(obs store.ObservedAudiobook) bool {
	sameMTime := e.mtime.UTC().Truncate(time.Microsecond).
		Equal(obs.MTime.UTC().Truncate(time.Microsecond))
	sameCueMTime := (e.cueMTime == nil && obs.CueMTime == nil) ||
		(e.cueMTime != nil && obs.CueMTime != nil &&
			e.cueMTime.UTC().Truncate(time.Microsecond).Equal(obs.CueMTime.UTC().Truncate(time.Microsecond)))
	return e.relativePath != obs.RelativePath ||
		e.sizeBytes != obs.SizeBytes ||
		!sameMTime ||
		e.sha256 != obs.ContentSHA256 ||
		!sameCueMTime ||
		e.cueSizeBytes != obs.CueSizeBytes ||
		e.title != obs.Title ||
		!bytes.Equal([]byte(e.authorsJSON), normJSONSlice(obs.AuthorsJSON)) ||
		!bytes.Equal([]byte(e.narrsJSON), normJSONSlice(obs.NarratorsJSON)) ||
		e.seriesName != obs.SeriesName ||
		e.asin != obs.ASIN ||
		e.isbn != obs.ISBN ||
		e.duration != obs.DurationSeconds ||
		!bytes.Equal([]byte(e.chaptersJSON), normJSONSlice(obs.ChaptersJSON))
}

func normJSONSlice(b []byte) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

func observationAudiobookKey(obs store.ObservedAudiobook, byCalibreID bool) (string, error) {
	if byCalibreID {
		if obs.CalibreID == nil {
			return "", errors.New("calibre audiobook observation missing calibre_id")
		}
		return strconv.FormatInt(*obs.CalibreID, 10), nil
	}
	if obs.RelativePath == "" {
		return "", errors.New("plain audiobook observation missing relative_path")
	}
	return obs.RelativePath, nil
}

func existingAudiobooksTx(ctx context.Context, tx *sql.Tx, folderID string, byCalibreID bool) (map[string]existingAudiobook, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, status, relative_path, calibre_id, size_bytes, mtime, content_sha256,
		        cue_mtime, cue_size_bytes, title, authors_json, narrators_json,
		        series_name, asin, isbn, duration_seconds, chapters_json
		   FROM catalog_audiobooks WHERE folder_id = ?`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]existingAudiobook{}
	for rows.Next() {
		var (
			e         existingAudiobook
			status    string
			calibreID sql.NullInt64
			mtime     string
			cueMTime  sql.NullString
		)
		if err := rows.Scan(&e.id, &status, &e.relativePath, &calibreID,
			&e.sizeBytes, &mtime, &e.sha256, &cueMTime, &e.cueSizeBytes,
			&e.title, &e.authorsJSON, &e.narrsJSON, &e.seriesName, &e.asin, &e.isbn,
			&e.duration, &e.chaptersJSON); err != nil {
			return nil, err
		}
		e.status = store.BookStatus(status)
		if calibreID.Valid {
			id := calibreID.Int64
			e.calibreID = &id
		}
		var err error
		if e.mtime, err = parseTime(mtime); err != nil {
			return nil, err
		}
		if e.cueMTime, err = parseTimePtr(cueMTime); err != nil {
			return nil, err
		}
		key := e.relativePath
		if byCalibreID {
			if e.calibreID == nil {
				continue
			}
			key = strconv.FormatInt(*e.calibreID, 10)
		}
		out[key] = e
	}
	return out, rows.Err()
}

func (s *Store) ReconcileFolderAudiobooks(
	ctx context.Context, folderID string, observed []store.ObservedAudiobook,
	complete bool, at time.Time,
) (store.ReconcileResult, error) {
	var result store.ReconcileResult
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var kind string
		if err := tx.QueryRowContext(ctx,
			`SELECT kind FROM folders WHERE id = ?`, folderID).Scan(&kind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return store.ErrNotFound
			}
			return err
		}
		byCalibreID := store.FolderKind(kind) == store.FolderCalibre

		existing, err := existingAudiobooksTx(ctx, tx, folderID, byCalibreID)
		if err != nil {
			return err
		}

		if byCalibreID && len(observed) > 0 {
			if _, err := tx.ExecContext(ctx,
				`UPDATE catalog_audiobooks SET relative_path = char(10) || id
				  WHERE folder_id = ?`, folderID); err != nil {
				return err
			}
		}

		seen := map[string]bool{}
		wrote := map[string]bool{}
		stamp := formatTime(at)

		for _, obs := range observed {
			key, err := observationAudiobookKey(obs, byCalibreID)
			if err != nil {
				return err
			}
			prior, had := existing[key]

			if obs.Unservable {
				if had {
					if prior.status == store.BookActive {
						if _, err := tx.ExecContext(ctx,
							`UPDATE catalog_audiobooks
							    SET status = 'missing', absent_at = ?, updated_at = ?
							  WHERE id = ? AND folder_id = ?`,
							stamp, stamp, prior.id, folderID); err != nil {
							return err
						}
						result.Missing++
					}
				}
				seen[key] = true
				continue
			}

			if had && obs.Replaces {
				if _, err := tx.ExecContext(ctx,
					`DELETE FROM catalog_audiobooks WHERE id = ? AND folder_id = ?`,
					prior.id, folderID); err != nil {
					return err
				}
				had = false
				result.Replaced++
			}

			var cueMTime any
			if obs.CueMTime != nil {
				cueMTime = formatTime(*obs.CueMTime)
			}

			switch {
			case had && obs.Unchanged:
				returned := prior.status == store.BookMissing
				upAt := ""
				if returned {
					upAt = ", updated_at = " + fmt.Sprintf("%q", stamp)
				}
				if _, err := tx.ExecContext(ctx,
					`UPDATE catalog_audiobooks
					    SET status = 'active', relative_path = ?, size_bytes = ?, mtime = ?,
					        cue_mtime = ?, cue_size_bytes = ?, seen_at = ?, absent_at = NULL`+upAt+`
					  WHERE id = ? AND folder_id = ?`,
					obs.RelativePath, obs.SizeBytes, formatTime(obs.MTime),
					cueMTime, obs.CueSizeBytes, stamp, prior.id, folderID); err != nil {
					return err
				}
				if returned {
					result.Returned++
				}
				seen[key] = true
				wrote[key] = true
				continue

			case had:
				returned := prior.status == store.BookMissing
				changed := prior.differsFrom(obs)
				if _, err := tx.ExecContext(ctx,
					`UPDATE catalog_audiobooks
					    SET status = 'active', relative_path = ?, calibre_id = ?,
					        size_bytes = ?, mtime = ?, content_sha256 = ?,
					        cue_mtime = ?, cue_size_bytes = ?,
					        title = ?, authors_json = ?, narrators_json = ?,
					        series_name = ?, asin = ?, isbn = ?,
					        duration_seconds = ?, chapters_json = ?,
					        seen_at = ?, absent_at = NULL
					  WHERE id = ? AND folder_id = ?`,
					obs.RelativePath, obs.CalibreID,
					obs.SizeBytes, formatTime(obs.MTime), obs.ContentSHA256,
					cueMTime, obs.CueSizeBytes,
					obs.Title, string(normJSONSlice(obs.AuthorsJSON)), string(normJSONSlice(obs.NarratorsJSON)),
					obs.SeriesName, obs.ASIN, obs.ISBN,
					obs.DurationSeconds, string(normJSONSlice(obs.ChaptersJSON)),
					stamp, prior.id, folderID); err != nil {
					return err
				}
				if returned {
					result.Returned++
				}
				if changed {
					result.Updated++
				}
				if changed || returned {
					if _, err := tx.ExecContext(ctx,
						`UPDATE catalog_audiobooks SET updated_at = ? WHERE id = ? AND folder_id = ?`,
						stamp, prior.id, folderID); err != nil {
						return err
					}
				}

			default:
				id := store.NewID()
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO catalog_audiobooks (
						id, folder_id, status, relative_path, calibre_id,
						size_bytes, mtime, content_sha256, cue_mtime, cue_size_bytes,
						title, authors_json, narrators_json, series_name, asin, isbn,
						duration_seconds, chapters_json, seen_at, absent_at, created_at, updated_at
					) VALUES (?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
					id, folderID, obs.RelativePath, obs.CalibreID,
					obs.SizeBytes, formatTime(obs.MTime), obs.ContentSHA256, cueMTime, obs.CueSizeBytes,
					obs.Title, string(normJSONSlice(obs.AuthorsJSON)), string(normJSONSlice(obs.NarratorsJSON)),
					obs.SeriesName, obs.ASIN, obs.ISBN,
					obs.DurationSeconds, string(normJSONSlice(obs.ChaptersJSON)),
					stamp, stamp, stamp); err != nil {
					return err
				}
				if !obs.Replaces {
					result.Added++
				}
			}

			seen[key] = true
			wrote[key] = true
		}

		if byCalibreID && len(observed) > 0 {
			for key, prior := range existing {
				if wrote[key] {
					continue
				}
				if _, err := tx.ExecContext(ctx,
					`UPDATE catalog_audiobooks SET relative_path = ? WHERE id = ? AND folder_id = ?
					 AND NOT EXISTS (
					   SELECT 1 FROM catalog_audiobooks other
					   WHERE other.folder_id = ? AND other.relative_path = ? AND other.id <> ?)`,
					prior.relativePath, prior.id, folderID, folderID, prior.relativePath, prior.id); err != nil {
					return err
				}
			}
		}

		// Completeness and non-empty guard: never mark missing or purge on an
		// incomplete or empty observation set.
		if !complete || len(observed) == 0 {
			return nil
		}

		for key, prior := range existing {
			if seen[key] {
				continue
			}
			if byCalibreID {
				if _, err := tx.ExecContext(ctx,
					`DELETE FROM catalog_audiobooks WHERE id = ? AND folder_id = ?`,
					prior.id, folderID); err != nil {
					return err
				}
				result.Purged++
				continue
			}
			if prior.status == store.BookActive {
				if _, err := tx.ExecContext(ctx,
					`UPDATE catalog_audiobooks
					    SET status = 'missing', absent_at = ?, updated_at = ?
					  WHERE id = ? AND folder_id = ?`,
					stamp, stamp, prior.id, folderID); err != nil {
					return err
				}
				result.Missing++
			}
		}
		return nil
	})
	return result, err
}

func (s *Store) CatalogAudiobookByID(ctx context.Context, viewerID, audiobookID string) (store.CatalogAudiobook, error) {
	query := `SELECT ` + catalogAudiobookColumns + ` FROM catalog_audiobooks ca WHERE ca.id = ?`
	args := []any{audiobookID}
	if viewerID != "" {
		query += ` AND EXISTS (SELECT 1 FROM user_folders uf
			WHERE uf.folder_id = ca.folder_id AND uf.user_id = ?)`
		args = append(args, viewerID)
	}
	row := s.db.QueryRowContext(ctx, query, args...)
	ab, err := scanCatalogAudiobook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.CatalogAudiobook{}, store.ErrNotFound
	}
	return ab, err
}

func (s *Store) ActiveCatalogAudiobooks(ctx context.Context) ([]store.CatalogAudiobook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+catalogAudiobookColumns+`
		   FROM catalog_audiobooks ca
		  WHERE ca.status = 'active'
		  ORDER BY ca.created_at, ca.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.CatalogAudiobook{}
	for rows.Next() {
		ab, err := scanCatalogAudiobook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ab)
	}
	return out, rows.Err()
}

func (s *Store) BookAudiobookByBookID(ctx context.Context, viewerID, bookID string) (store.BookAudiobook, store.CatalogAudiobook, error) {
	query := `SELECT ba.book_id, ba.audiobook_id, ba.edition_sha, ba.audiobook_sha,
	                 ba.source, ba.state, ba.mapping_key, ba.mapping_json, ba.updated_at,
	                 ` + catalogAudiobookColumns + `
	            FROM book_audiobooks ba
	            JOIN books b ON b.id = ba.book_id
	            JOIN catalog_audiobooks ca ON ca.id = ba.audiobook_id
	           WHERE ba.book_id = ?`
	args := []any{bookID}
	if viewerID != "" {
		query += ` AND EXISTS (SELECT 1 FROM user_folders uf
			WHERE uf.folder_id = b.folder_id AND uf.user_id = ?)
		   AND EXISTS (SELECT 1 FROM user_folders uf
			WHERE uf.folder_id = ca.folder_id AND uf.user_id = ?)`
		args = append(args, viewerID, viewerID)
	}
	var (
		ba                  store.BookAudiobook
		state, mapping, upS string
		ca                  store.CatalogAudiobook
		caStatus            string
		caCalibreID         sql.NullInt64
		caMTime             string
		caCueMTime          sql.NullString
		caAuthors, caNarrs  string
		caChapters          string
		caCreated, caUp     string
		caSeen, caAbsent    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&ba.BookID, &ba.AudiobookID, &ba.EditionSHA, &ba.AudiobookSHA,
		&ba.Source, &state, &ba.MappingKey, &mapping, &upS,
		&ca.ID, &ca.FolderID, &caStatus,
		&ca.RelativePath, &caCalibreID, &ca.SizeBytes, &caMTime, &ca.ContentSHA256,
		&caCueMTime, &ca.CueSizeBytes,
		&ca.Title, &caAuthors, &caNarrs, &ca.SeriesName, &ca.ASIN, &ca.ISBN,
		&ca.DurationSeconds, &caChapters,
		&caCreated, &caUp, &caSeen, &caAbsent,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return store.BookAudiobook{}, store.CatalogAudiobook{}, store.ErrNotFound
	}
	if err != nil {
		return store.BookAudiobook{}, store.CatalogAudiobook{}, err
	}
	ba.State = store.BookAudiobookState(state)
	if mapping != "" {
		ba.MappingJSON = []byte(mapping)
	}
	if ba.UpdatedAt, err = parseTime(upS); err != nil {
		return store.BookAudiobook{}, store.CatalogAudiobook{}, err
	}

	ca.Status = store.BookStatus(caStatus)
	if caCalibreID.Valid {
		id := caCalibreID.Int64
		ca.CalibreID = &id
	}
	ca.AuthorsJSON = []byte(caAuthors)
	ca.NarratorsJSON = []byte(caNarrs)
	ca.ChaptersJSON = []byte(caChapters)
	if ca.MTime, err = parseTime(caMTime); err != nil {
		return store.BookAudiobook{}, store.CatalogAudiobook{}, err
	}
	if ca.CueMTime, err = parseTimePtr(caCueMTime); err != nil {
		return store.BookAudiobook{}, store.CatalogAudiobook{}, err
	}
	if ca.CreatedAt, err = parseTime(caCreated); err != nil {
		return store.BookAudiobook{}, store.CatalogAudiobook{}, err
	}
	if ca.UpdatedAt, err = parseTime(caUp); err != nil {
		return store.BookAudiobook{}, store.CatalogAudiobook{}, err
	}
	if ca.SeenAt, err = parseTimePtr(caSeen); err != nil {
		return store.BookAudiobook{}, store.CatalogAudiobook{}, err
	}
	if ca.AbsentAt, err = parseTimePtr(caAbsent); err != nil {
		return store.BookAudiobook{}, store.CatalogAudiobook{}, err
	}
	return ba, ca, nil
}

func (s *Store) ListBookAudiobooks(ctx context.Context) ([]store.BookAudiobook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT book_id, audiobook_id, edition_sha, audiobook_sha,
		        source, state, mapping_key, mapping_json, updated_at
		   FROM book_audiobooks
		  ORDER BY book_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.BookAudiobook{}
	for rows.Next() {
		var (
			ba                  store.BookAudiobook
			state, mapping, upS string
		)
		if err := rows.Scan(&ba.BookID, &ba.AudiobookID, &ba.EditionSHA, &ba.AudiobookSHA,
			&ba.Source, &state, &ba.MappingKey, &mapping, &upS); err != nil {
			return nil, err
		}
		ba.State = store.BookAudiobookState(state)
		if mapping != "" {
			ba.MappingJSON = []byte(mapping)
		}
		var err error
		if ba.UpdatedAt, err = parseTime(upS); err != nil {
			return nil, err
		}
		out = append(out, ba)
	}
	return out, rows.Err()
}

func (s *Store) UpsertBookAudiobook(ctx context.Context, link store.BookAudiobook, at time.Time) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM book_audiobooks WHERE audiobook_id = ? AND book_id <> ?`,
			link.AudiobookID, link.BookID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO book_audiobooks (
				book_id, audiobook_id, edition_sha, audiobook_sha,
				source, state, mapping_key, mapping_json, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (book_id) DO UPDATE SET
				audiobook_id  = excluded.audiobook_id,
				edition_sha   = excluded.edition_sha,
				audiobook_sha = excluded.audiobook_sha,
				source        = excluded.source,
				state         = excluded.state,
				mapping_key   = excluded.mapping_key,
				mapping_json  = excluded.mapping_json,
				updated_at    = excluded.updated_at`,
			link.BookID, link.AudiobookID, link.EditionSHA, link.AudiobookSHA,
			link.Source, string(link.State), link.MappingKey, string(link.MappingJSON), formatTime(at))
		return err
	})
}

func (s *Store) DeleteBookAudiobook(ctx context.Context, bookID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM book_audiobooks WHERE book_id = ?`, bookID)
	return err
}
