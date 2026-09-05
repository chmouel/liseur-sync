package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Store) BooksInFolder(ctx context.Context, folderID string) ([]store.KnownBook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, status, relative_path, size_bytes, mtime,
		        content_sha256, calibre_id, cover_sha256
		 FROM books WHERE folder_id = ? ORDER BY relative_path`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := []store.KnownBook{}
	for rows.Next() {
		var (
			b         store.KnownBook
			status    string
			mtime     string
			calibreID sql.NullInt64
			coverSHA  sql.NullString
		)
		if err := rows.Scan(&b.ID, &status, &b.RelativePath, &b.SizeBytes,
			&mtime, &b.ContentSHA256, &calibreID, &coverSHA); err != nil {
			return nil, err
		}
		b.Status = store.BookStatus(status)
		if calibreID.Valid {
			id := calibreID.Int64
			b.CalibreID = &id
		}
		b.CoverSHA256 = coverSHA.String
		var err error
		if b.MTime, err = parseTime(mtime); err != nil {
			return nil, err
		}
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
			`SELECT kind FROM folders WHERE id = ?`, folderID).Scan(&kind); err != nil {
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
		// Drain old bookkeeping before rekeying so an alias held only by a
		// disposable orphan does not block the mapped work from learning the
		// book's current digest. The same sweep runs after purge for works
		// orphaned by this transaction.
		if byCalibreID && complete && len(observed) > 0 {
			if err := collectEmptyWorksTx(ctx, tx); err != nil {
				return err
			}
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
				`UPDATE books SET relative_path = char(10) || id
				 WHERE folder_id = ?`, folderID); err != nil {
				return err
			}
		}

		seen := map[string]bool{}
		// A parked path is only put back by a write, so the pass has to
		// remember which books it wrote rather than which it saw.
		wrote := map[string]bool{}
		for _, obs := range observed {
			key, err := observationKey(obs, byCalibreID)
			if err != nil {
				return err
			}
			prior, had := existing[key]

			// A book the folder's catalog still lists but has no
			// servable file for is seen, not gone: it is marked missing
			// and kept, and being in `seen` is what keeps the purge off
			// it. Nothing else is written, because such an observation
			// carries an identity and nothing more.
			if obs.Unservable {
				if had {
					lost, err := markBookMissingTx(ctx, tx, folderID, prior.id, at)
					if err != nil {
						return err
					}
					if lost {
						result.Missing++
					}
				}
				seen[key] = true
				continue
			}

			// Rule 4: content change is not identity transfer. The old
			// row goes, taking its identifiers, relations and the
			// user_book_works mapping of everyone who was reading it,
			// because whatever was copied over that path is not the
			// book they were reading.
			if had && obs.Replaces {
				if err := deleteBookTx(ctx, tx, folderID, prior.id); err != nil {
					return err
				}
				had = false
				result.Replaced++
			}

			var bookID string
			// refreshing marks the update path, where Updated is only
			// counted if the row's facts or its relations actually
			// moved — a pass that re-read everything and found it as
			// recorded changed nothing worth a log line.
			refreshing := false
			factsChanged := false
			statusReturned := false
			switch {
			case had && obs.Unchanged:
				// The pass recognised this file by its stat and did not
				// re-read it, so it carries no metadata to write. Only
				// the stat and the status are refreshed; overwriting the
				// title with the nothing in hand would empty the catalog
				// on the second pass.
				returned, err := touchBookTx(ctx, tx, folderID, prior.id, obs, at)
				if err != nil {
					return err
				}
				if returned {
					result.Returned++
				}
				seen[key] = true
				wrote[key] = true
				continue
			case had:
				bookID = prior.id
				returned, err := updateBookTx(ctx, tx, folderID, prior.id, obs, at)
				if err != nil {
					return err
				}
				if returned {
					result.Returned++
					statusReturned = true
				}
				// The same book with different bytes: a Calibre metadata
				// edit rewrites the publication in place. Whoever was
				// reading it keeps their work, and the work learns the
				// digest their device will report next.
				rekeyed, err := registerReaderDigestTx(
					ctx, tx, prior.id, prior.sha256, obs)
				if err != nil {
					return err
				}
				result.Rekeyed += rekeyed
				refreshing = true
				factsChanged = prior.facts.DiffersFrom(obs)
			default:
				bookID = store.NewID()
				if err := insertBookTx(ctx, tx, folderID, bookID, obs, at); err != nil {
					return err
				}
				if !obs.Replaces {
					result.Added++
				}
			}

			relationsChanged, err := replaceRelationsTx(ctx, tx, folderID, bookID, obs, at, refreshing)
			if err != nil {
				return err
			}
			if refreshing && (factsChanged || relationsChanged) {
				result.Updated++
			}
			// updated_at is a modification time clients see (catalog
			// JSON, OPDS, conditional GETs), so a pass that merely
			// re-read the book must not advance it. A return counts:
			// missing→active is a visible change even when every fact
			// matches.
			if refreshing && (factsChanged || relationsChanged || statusReturned) {
				if _, err := tx.ExecContext(ctx,
					`UPDATE books SET updated_at = ? WHERE id = ? AND folder_id = ?`,
					formatTime(at), bookID, folderID); err != nil {
					return err
				}
			}
			if err := reindexBookTx(ctx, tx, bookID); err != nil {
				return err
			}
			seen[key] = true
			wrote[key] = true
		}

		// Whatever the pass did not write is still parked under the
		// placeholder path, which is a path no open can ever resolve.
		// Putting the old one back is what keeps a book this pass could
		// not read — unservable, or behind a permission error — openable
		// again the moment it can.
		if byCalibreID && len(observed) > 0 {
			if err := restoreParkedPathsTx(ctx, tx, folderID, existing, wrote); err != nil {
				return err
			}
		}

		// Rules 1 and 2. A pass that hit a read error, a parse failure
		// or a scan bound does not know what it did not see; a pass that
		// observed nothing is indistinguishable from an unmounted mount
		// point, which is still readable and still empty. In both cases
		// the honest answer is to record what was seen and conclude
		// nothing about what was not.
		if !complete || len(observed) == 0 {
			return collectOrphanEntitiesTx(ctx, tx)
		}
		// A Calibre folder's metadata.db is a catalog somebody curates,
		// so a book a complete pass no longer finds in it was removed
		// rather than misplaced, and keeping the row forever leaves the
		// reader a tile for a book this server will never serve again
		// (ADR-0022). Everywhere else absence is only ever evidence
		// about a disk, and the row is kept.
		if byCalibreID {
			purged, err := purgeUnseenTx(ctx, tx, folderID, seen)
			if err != nil {
				return err
			}
			result.Purged = purged
			return collectOrphanEntitiesTx(ctx, tx)
		}
		missing, err := markMissingTx(ctx, tx, folderID, seen, byCalibreID, at)
		if err != nil {
			return err
		}
		result.Missing += missing
		return collectOrphanEntitiesTx(ctx, tx)
	})
	return result, err
}

// collectOrphanEntitiesTx deletes every entity no book claims any more.
//
// Entities are library-wide (ADR-0019), so a name does not go when the
// folder that introduced it goes — it goes when its last membership
// does. A claim counts as a membership: a series a reader filed a book
// into is theirs whether or not a pass has ever observed it, and
// collecting it would throw the claim away with it.
func collectOrphanEntitiesTx(ctx context.Context, tx *sql.Tx) error {
	kinds := []struct{ table, membership, column string }{
		{"series", "book_series", "series_id"},
		{"tags", "book_tags", "tag_id"},
		{"contributors", "book_contributors", "contributor_id"},
	}
	for _, k := range kinds {
		statement := `DELETE FROM ` + k.table + `
			 WHERE NOT EXISTS (
			     SELECT 1 FROM ` + k.membership + ` m
			      WHERE m.` + k.column + ` = ` + k.table + `.id)`
		if k.table == "series" {
			statement += `
			   AND NOT EXISTS (
			     SELECT 1 FROM book_series_override_items i
			      WHERE i.series_id = series.id)`
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
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

// priorBook is what the catalog already holds for one identity key: the
// row's id, its material facts, and the digest it was last written
// with. The digest is carried because a book whose bytes changed while
// keeping its identity — the shape of every Calibre metadata edit — is
// the one case where a reader's work graph has to be told something.
// The facts are read here, before a Calibre pass parks every path, so
// they describe the row as the last pass left it rather than the parked
// intermediate state.
type priorBook struct {
	id     string
	path   string
	sha256 string
	facts  store.BookFacts
}

func existingBooksTx(
	ctx context.Context, tx *sql.Tx, folderID string, byCalibreID bool,
) (map[string]priorBook, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, relative_path, calibre_id, content_sha256,
		        size_bytes, mtime, original_filename, media_type,
		        cover_relative_path, cover_sha256,
		        title, subtitle, description, publisher, published_date
		 FROM books WHERE folder_id = ?`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]priorBook{}
	for rows.Next() {
		var (
			id, path, sha string
			calibreID     sql.NullInt64
			mtime         string
			cover         sql.NullString
			coverSHA      sql.NullString
			facts         store.BookFacts
		)
		if err := rows.Scan(&id, &path, &calibreID, &sha,
			&facts.SizeBytes, &mtime, &facts.OriginalFilename, &facts.MediaType,
			&cover, &coverSHA,
			&facts.Title, &facts.Subtitle, &facts.Description,
			&facts.Publisher, &facts.PublishedDate); err != nil {
			return nil, err
		}
		if facts.MTime, err = parseTime(mtime); err != nil {
			return nil, err
		}
		facts.RelativePath = path
		facts.ContentSHA256 = sha
		facts.CoverRelativePath = cover.String
		facts.CoverSHA256 = coverSHA.String
		prior := priorBook{id: id, path: path, sha256: sha, facts: facts}
		switch {
		case byCalibreID && calibreID.Valid:
			out[fmt.Sprintf("c:%d", calibreID.Int64)] = prior
		case !byCalibreID:
			out["p:"+path] = prior
		}
	}
	return out, rows.Err()
}

func deleteBookTx(ctx context.Context, tx *sql.Tx, folderID, bookID string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM book_search WHERE book_id = ? AND folder_id = ?`,
		bookID, folderID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`DELETE FROM books WHERE id = ? AND folder_id = ?`, bookID, folderID)
	return err
}

func insertBookTx(
	ctx context.Context, tx *sql.Tx, folderID, bookID string,
	obs store.ObservedBook, at time.Time,
) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO books (
			id, folder_id, status,
			relative_path, size_bytes, mtime, content_sha256,
			original_filename, media_type, calibre_id,
			cover_relative_path, cover_sha256,
			title, subtitle, description, publisher, published_date,
			created_at, updated_at, seen_at, absent_at)
		 VALUES (?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		bookID, folderID,
		obs.RelativePath, obs.SizeBytes, formatTime(obs.MTime), obs.ContentSHA256,
		obs.OriginalFilename, mediaTypeOf(obs), nullInt64(obs.CalibreID),
		nullStr(obs.CoverRelativePath), obs.CoverSHA256,
		obs.Title, obs.Subtitle, obs.Description, obs.Publisher, obs.PublishedDate,
		formatTime(at), formatTime(at), formatTime(at))
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
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM books WHERE id = ? AND folder_id = ?`,
		bookID, folderID).Scan(&status); err != nil {
		return false, err
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE books SET
			status = 'active',
			relative_path = ?, size_bytes = ?, mtime = ?, content_sha256 = ?,
			original_filename = ?, media_type = ?, calibre_id = ?,
			cover_relative_path = ?, cover_sha256 = ?,
			title = ?, subtitle = ?, description = ?, publisher = ?,
			published_date = ?, seen_at = ?, absent_at = NULL
		 WHERE id = ? AND folder_id = ?`,
		obs.RelativePath, obs.SizeBytes, formatTime(obs.MTime), obs.ContentSHA256,
		obs.OriginalFilename, mediaTypeOf(obs), nullInt64(obs.CalibreID),
		nullStr(obs.CoverRelativePath), obs.CoverSHA256,
		obs.Title, obs.Subtitle, obs.Description, obs.Publisher, obs.PublishedDate,
		formatTime(at),
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
	for key, prior := range existing {
		if seen[key] {
			continue
		}
		lost, err := markBookMissingTx(ctx, tx, folderID, prior.id, at)
		if err != nil {
			return 0, err
		}
		if lost {
			count++
		}
	}
	return count, nil
}

// restoreParkedPathsTx puts back the paths of the books a Calibre pass
// parked but never wrote.
//
// Every path in the folder is moved under an unreachable placeholder
// before a pass writes its new ones, so that two books swapping
// directories never make the unique index see both wanting the same
// path. A book the pass then did not write — one Calibre still lists but
// has no servable file for, one behind a read error — would be left
// holding the placeholder, which is a path that resolves to nothing.
//
// A path another book has taken in the meantime is left parked rather
// than forced: the row is unreachable either way, and the alternative is
// a failed pass.
func restoreParkedPathsTx(
	ctx context.Context, tx *sql.Tx, folderID string,
	existing map[string]priorBook, wrote map[string]bool,
) error {
	for key, prior := range existing {
		if wrote[key] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE books SET relative_path = ?
			  WHERE id = ? AND folder_id = ?
			    AND NOT EXISTS (
			        SELECT 1 FROM books other
			         WHERE other.folder_id = ? AND other.relative_path = ?
			           AND other.id <> ?)`,
			prior.path, prior.id, folderID, folderID, prior.path, prior.id); err != nil {
			return err
		}
	}
	return nil
}

// markBookMissingTx flags one book absent and reports whether that
// changed anything, so a book already known to be gone is not counted
// again on every pass.
func markBookMissingTx(
	ctx context.Context, tx *sql.Tx, folderID, bookID string, at time.Time,
) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE books SET status = 'missing', absent_at = ?, updated_at = ?
		 WHERE id = ? AND folder_id = ? AND status = 'active'`,
		formatTime(at), formatTime(at), bookID, folderID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// purgeUnseenTx deletes the books a complete Calibre pass did not find
// in metadata.db, then collects every established work with no catalog
// mapping or reading history.
//
// The sweep includes orphans left by earlier book deletion and replacement
// behavior, but not pending sync work. A work with one op, one session or one
// rollup is somebody's reading and survives its file as a work with no book
// here.
func purgeUnseenTx(
	ctx context.Context, tx *sql.Tx, folderID string, seen map[string]bool,
) (int, error) {
	existing, err := existingBooksTx(ctx, tx, folderID, true)
	if err != nil {
		return 0, err
	}
	count := 0
	for key, prior := range existing {
		if seen[key] {
			continue
		}
		if err := deleteBookTx(ctx, tx, folderID, prior.id); err != nil {
			return 0, err
		}
		count++
	}
	if err := collectEmptyWorksTx(ctx, tx); err != nil {
		return 0, err
	}
	return count, nil
}

func collectEmptyWorksTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM works
		  WHERE pending = 0
		    AND NOT EXISTS (SELECT 1 FROM user_book_works m
		                     WHERE m.user_id = works.user_id AND m.work_id = works.id)
		    AND NOT EXISTS (SELECT 1 FROM ops o
		                     WHERE o.user_id = works.user_id AND o.work_id = works.id)
		    AND NOT EXISTS (SELECT 1 FROM sessions s
		                     WHERE s.user_id = works.user_id AND s.work_id = works.id)
		    AND NOT EXISTS (SELECT 1 FROM session_rollups r
		                     WHERE r.user_id = works.user_id AND r.work_id = works.id)
		    AND NOT EXISTS (SELECT 1 FROM session_rollups_v2 r
		                     WHERE r.user_id = works.user_id AND r.work_id = works.id)`)
	return err
}

// registerReaderDigestTx teaches every reader of a book the digest its
// bytes now have.
//
// Calibre rewrites a publication whenever it embeds edited metadata, so
// the file a device will hash next is not the file the reader's work was
// resolved from. Without this the next sync matches nothing and mints a
// second work, which is a duplicate tile beside the book it belongs to.
//
// Registration is additive, never a rewrite: the old digest keeps
// naming the same work, so a device still holding the old file is
// unaffected and no op or session has to be re-pointed at a new edition.
// The new title/author fingerprint is registered the same way, because a
// title edit moves that alias too.
//
// A value another work already claims is left exactly as it is. Two
// works meeting over one digest is a merge, and a merge is a decision
// with reading history on both sides — not something a scan does behind
// the reader's back.
func registerReaderDigestTx(
	ctx context.Context, tx *sql.Tx, bookID, priorSHA string, obs store.ObservedBook,
) (int, error) {
	if obs.ContentSHA256 == "" || obs.ContentSHA256 == priorSHA {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT user_id, work_id FROM user_book_works WHERE book_id = ?`, bookID)
	if err != nil {
		return 0, err
	}
	type userWork struct{ userID, workID string }
	var readers []userWork
	for rows.Next() {
		var uw userWork
		if err := rows.Scan(&uw.userID, &uw.workID); err != nil {
			rows.Close()
			return 0, err
		}
		readers = append(readers, uw)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	fingerprint := metadata.TitleAuthorFingerprint(obs.Title, primaryAuthorOf(obs))
	count := 0
	for _, uw := range readers {
		moved, err := registerDigestIfFreeTx(
			ctx, tx, uw.userID, obs.ContentSHA256, uw.workID)
		if err != nil {
			return 0, err
		}
		if fingerprint != "" {
			added, err := addAliasIfFreeTx(ctx, tx, uw.userID, "ta", fingerprint, uw.workID)
			if err != nil {
				return 0, err
			}
			moved = moved || added
		}
		if moved {
			count++
		}
	}
	return count, nil
}

// registerDigestIfFreeTx registers the sha256 alias and edition as one unit.
// Either table can already carry a digest, so neither row is added when the
// other one belongs to a different work.
func registerDigestIfFreeTx(
	ctx context.Context, tx *sql.Tx, userID, sha, workID string,
) (bool, error) {
	var aliasOwner, editionOwner string
	aliasErr := tx.QueryRowContext(ctx,
		`SELECT work_id FROM aliases WHERE user_id = ? AND kind = 'sha256' AND value = ?`,
		userID, sha).Scan(&aliasOwner)
	if aliasErr != nil && !errors.Is(aliasErr, sql.ErrNoRows) {
		return false, aliasErr
	}
	editionErr := tx.QueryRowContext(ctx,
		`SELECT work_id FROM editions WHERE user_id = ? AND sha256 = ?`,
		userID, sha).Scan(&editionOwner)
	if editionErr != nil && !errors.Is(editionErr, sql.ErrNoRows) {
		return false, editionErr
	}
	if aliasErr == nil && aliasOwner != workID ||
		editionErr == nil && editionOwner != workID {
		return false, nil
	}

	added := false
	if errors.Is(aliasErr, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO aliases (user_id, kind, value, work_id)
			 VALUES (?, 'sha256', ?, ?)`, userID, sha, workID); err != nil {
			return false, err
		}
		added = true
	}
	if errors.Is(editionErr, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO editions (user_id, sha256, work_id) VALUES (?, ?, ?)`,
			userID, sha, workID); err != nil {
			return false, err
		}
		added = true
	}
	return added, nil
}

// addAliasIfFreeTx registers one alias, and says so, unless somebody
// already holds that name.
func addAliasIfFreeTx(
	ctx context.Context, tx *sql.Tx, userID, kind, value, workID string,
) (bool, error) {
	var owner string
	err := tx.QueryRowContext(ctx,
		`SELECT work_id FROM aliases WHERE user_id = ? AND kind = ? AND value = ?`,
		userID, kind, value).Scan(&owner)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO aliases (user_id, kind, value, work_id) VALUES (?, ?, ?, ?)`,
		userID, kind, value, workID); err != nil {
		return false, err
	}
	return true, nil
}

// primaryAuthorOf is the name a fingerprint is built from: the first
// author the observation lists, which is the same one the catalog
// credits a book to.
func primaryAuthorOf(obs store.ObservedBook) string {
	for _, c := range obs.Contributors {
		if c.Role == "" || c.Role == store.ContributorRoleAuthor {
			return c.Name
		}
	}
	return ""
}

// replaceRelationsTx rewrites a book's metadata sets wholesale. With no
// manual editing and no external providers there is one source per
// folder and nothing to merge, so "what the folder says now" simply
// replaces "what it said last time". It reports whether the rewrite
// left different rows behind, because that difference — not the rewrite
// itself — is what a pass counts as an update.
func replaceRelationsTx(
	ctx context.Context, tx *sql.Tx, folderID, bookID string,
	obs store.ObservedBook, at time.Time, detectChange bool,
) (bool, error) {
	// The fingerprints cost ten SELECTs per book, so only the refresh
	// path — the one that reports Updated — pays for them; an insert
	// already counted as Added.
	var before string
	if detectChange {
		var err error
		if before, err = relationsFingerprintTx(ctx, tx, folderID, bookID); err != nil {
			return false, err
		}
	}
	for _, table := range []string{
		"book_identifiers", "book_languages", "book_tags",
		"book_series", "book_contributors",
	} {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE folder_id = ? AND book_id = ?`,
			folderID, bookID); err != nil {
			return false, err
		}
	}

	for _, id := range obs.Identifiers {
		if id.Scheme == "" || id.Value == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_identifiers (folder_id, book_id, scheme, value)
			 VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			folderID, bookID, id.Scheme, id.Value); err != nil {
			return false, err
		}
	}
	for _, lang := range obs.Languages {
		if lang == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_languages (folder_id, book_id, language)
			 VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
			folderID, bookID, lang); err != nil {
			return false, err
		}
	}
	for _, tag := range obs.Tags {
		tagID, err := resolveEntityTx(ctx, tx, "tags", tag, at)
		if err != nil || tagID == "" {
			if err != nil {
				return false, err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_tags (folder_id, book_id, tag_id)
			 VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
			folderID, bookID, tagID); err != nil {
			return false, err
		}
	}
	for _, sr := range obs.Series {
		seriesID, err := resolveSeriesTx(ctx, tx, folderID, sr.Name, at)
		if err != nil || seriesID == "" {
			if err != nil {
				return false, err
			}
			continue
		}
		var position any
		if sr.Position != nil {
			position = *sr.Position
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_series (folder_id, book_id, series_id, position)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (book_id, series_id) DO UPDATE SET position = excluded.position`,
			folderID, bookID, seriesID, position); err != nil {
			return false, err
		}
	}
	for _, c := range obs.Contributors {
		contributorID, err := resolveEntityTx(ctx, tx, "contributors", c.Name, at)
		if err != nil || contributorID == "" {
			if err != nil {
				return false, err
			}
			continue
		}
		role := c.Role
		if role == "" {
			role = store.ContributorRoleAuthor
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO book_contributors
			     (folder_id, book_id, contributor_id, role, position)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (book_id, contributor_id, role)
			 DO UPDATE SET position = excluded.position`,
			folderID, bookID, contributorID, role, c.Position); err != nil {
			return false, err
		}
	}
	if !detectChange {
		return false, nil
	}
	after, err := relationsFingerprintTx(ctx, tx, folderID, bookID)
	if err != nil {
		return false, err
	}
	return before != after, nil
}

// relationsFingerprintQueries canonicalize one book's relation rows,
// one line per row, so before-and-after strings compare a rewrite for
// effect. Ordering is fixed and every query carries (folderID, bookID).
var relationsFingerprintQueries = []string{
	`SELECT scheme || ':' || value FROM book_identifiers
	  WHERE folder_id = ? AND book_id = ? ORDER BY scheme, value`,
	`SELECT language FROM book_languages
	  WHERE folder_id = ? AND book_id = ? ORDER BY language`,
	`SELECT tag_id FROM book_tags
	  WHERE folder_id = ? AND book_id = ? ORDER BY tag_id`,
	`SELECT series_id || '@' || COALESCE(CAST(position AS TEXT), '')
	   FROM book_series
	  WHERE folder_id = ? AND book_id = ? ORDER BY series_id`,
	`SELECT contributor_id || '#' || role || '#' || CAST(position AS TEXT)
	   FROM book_contributors
	  WHERE folder_id = ? AND book_id = ?
	  ORDER BY contributor_id, role`,
}

// relationsFingerprintTx reads one book's relations into a canonical
// string. It only ever compares a book with itself within one
// transaction on one backend, so the text of a REAL cast never has to
// agree across engines — only with itself.
func relationsFingerprintTx(
	ctx context.Context, tx *sql.Tx, folderID, bookID string,
) (string, error) {
	var out strings.Builder
	for i, query := range relationsFingerprintQueries {
		rows, err := tx.QueryContext(ctx, query, folderID, bookID)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				return "", err
			}
			fmt.Fprintf(&out, "%d:%s\n", i, line)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		rows.Close()
	}
	return out.String(), nil
}

// resolveSeriesTx resolves an observed series name to the series it
// belongs on, consulting the bindings a merge or a split wrote before
// falling back to the fold (ADR-0021).
//
// The most specific binding wins: this folder's, then the one that
// applies everywhere, then the ordinary match on the observed name. That
// order is what lets a folder-wise split disagree with the rest of the
// library about what `Essays` means, while a merge speaks for every
// folder at once.
//
// A binding whose series has been deleted cannot exist -- the foreign
// key cascades -- so a hit here always names a live shelf.
func resolveSeriesTx(
	ctx context.Context, tx *sql.Tx, folderID, name string, at time.Time,
) (string, error) {
	normalized := metadata.NormalizeName(name)
	if normalized == "" {
		return "", nil
	}
	var bound string
	err := tx.QueryRowContext(ctx,
		`SELECT series_id FROM series_bindings
		  WHERE normalized_name = ?
		    AND (folder_id = ? OR folder_id IS NULL)
		  ORDER BY folder_id IS NULL
		  LIMIT 1`,
		normalized, folderID).Scan(&bound)
	if err == nil {
		return bound, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return resolveEntityTx(ctx, tx, "series", name, at)
}

// resolveEntityTx finds or creates one library-wide entity by its
// normalized name, keeping the first spelling seen as the display value.
// An empty or whitespace-only name resolves to nothing rather than to an
// entity nobody can name.
//
// The name is the whole key (ADR-0019), so two folders holding the same
// series meet here and leave with one id.
func resolveEntityTx(
	ctx context.Context, tx *sql.Tx, table, name string, at time.Time,
) (string, error) {
	normalized := metadata.NormalizeName(name)
	if normalized == "" {
		return "", nil
	}
	var id string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM `+table+` WHERE normalized_name = ?`,
		normalized).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	// Passes over different folders can reach an unseen name at the same
	// moment, so the insert settles that rather than assuming it away:
	// whoever lost the race is handed the winner's id, and the display
	// spelling stays the one already stored.
	err = tx.QueryRowContext(ctx,
		`INSERT INTO `+table+` (id, name, normalized_name, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (normalized_name)
		 DO UPDATE SET normalized_name = excluded.normalized_name
		 RETURNING id`,
		store.NewID(), name, normalized, formatTime(at)).Scan(&id)
	return id, err
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
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM books WHERE id = ? AND folder_id = ?`,
		bookID, folderID).Scan(&status); err != nil {
		return false, err
	}
	returned := store.BookStatus(status) == store.BookMissing
	// A return is a visible change even though no fact moved, so it is
	// the one touch that advances the client-facing updated_at.
	if returned {
		_, err := tx.ExecContext(ctx,
			`UPDATE books SET status = 'active', relative_path = ?, size_bytes = ?,
			        mtime = ?, seen_at = ?, updated_at = ?, absent_at = NULL
			 WHERE id = ? AND folder_id = ?`,
			obs.RelativePath, obs.SizeBytes, formatTime(obs.MTime), formatTime(at),
			formatTime(at), bookID, folderID)
		return true, err
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE books SET status = 'active', relative_path = ?, size_bytes = ?,
		        mtime = ?, seen_at = ?, absent_at = NULL
		 WHERE id = ? AND folder_id = ?`,
		obs.RelativePath, obs.SizeBytes, formatTime(obs.MTime), formatTime(at),
		bookID, folderID)
	return false, err
}
