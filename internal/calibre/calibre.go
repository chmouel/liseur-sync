// Package calibre reads a Calibre library's metadata.db, read-only.
//
// It is the reading half of ADR-0014's third library source. Nothing
// here writes: not to the database, not to the tree around it. A Calibre
// library belongs to whoever curates it, and a sync server that edits
// somebody's metadata.db is a bug waiting for a version bump.
//
// The package is deliberately portable — no build tag — because it is
// only SQL and file reads. The scanning that uses it is Linux-only for
// its own reasons.
package calibre

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	// The same driver the SQLite store backend uses. It is pure Go and
	// already in the build, so reading somebody's Calibre library costs
	// no new dependency and no cgo.
	_ "modernc.org/sqlite"
)

// MetadataDB is the file that makes a directory a Calibre library.
const MetadataDB = "metadata.db"

// CoverName is what Calibre calls the cover it keeps beside each book.
const CoverName = "cover.jpg"

// OPFName is the per-book metadata document Calibre writes beside a
// book. It fills fields the database row does not carry; it is never a
// second way of discovering books.
const OPFName = "metadata.opf"

// Errors this package returns. They are distinguishable because a
// refresh reports them differently: a root that holds no Calibre
// library at all is a configuration mistake, and a database that cannot
// be read is an operational one.
var (
	// ErrNotCalibre means the root holds no metadata.db, or holds
	// something that is not a regular file by that name.
	ErrNotCalibre = errors.New("calibre: no metadata.db at the library root")
	// ErrUnsafeRoot means the database SQLite opened is not the file
	// that was checked at the root — a bind mount that moved, a root
	// that is itself a link into another library.
	ErrUnsafeRoot = errors.New("calibre: metadata.db is not the file at the root")
	// ErrUnsupportedSchema means the database has none of a table this
	// package cannot do without. A relation that only carries a field —
	// tags, languages, identifiers — costs that field instead, and is
	// recorded by Missing rather than raised here.
	ErrUnsupportedSchema = errors.New("calibre: unsupported metadata.db schema")
)

// Library is an open, read-only view of one Calibre library.
type Library struct {
	root string
	db   *sql.DB
	// missing records the relations this database does not have, so a
	// Calibre version that spells something differently costs the field
	// it carries rather than the whole refresh.
	missing []string
}

// Open opens the Calibre database at root for reading.
//
// The root is trusted input: an administrator names it, and anybody who
// can write inside it can already have the scanner ingest whatever they
// like (ADR-0014). The two checks here are against accident rather than
// against an attacker. metadata.db is lstat-ed through os.Root and
// refused unless it is a regular file, so a symlink by that name is
// never opened rather than being followed somewhere else; and the file
// SQLite reports having opened is compared with
// the one that was checked, so a root that turns out to be a link into
// somebody else's library is caught before a row is read. Neither
// closes the race between the check and SQLite's own open(2), and this
// comment is the only honest place to say so.
func Open(root string) (*Library, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("calibre: resolve root %q: %w", root, err)
	}
	rooted, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("calibre: open root %q: %w", absolute, err)
	}
	defer rooted.Close()
	// Lstat rather than Stat: a symlink named metadata.db is refused
	// here, before anything opens it. os.Root confines where a link may
	// point, and this is the separate question of whether the library
	// root is the plain directory it was described as.
	link, err := rooted.Lstat(MetadataDB)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotCalibre, err)
	}
	if !link.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file",
			ErrNotCalibre, MetadataDB)
	}
	file, err := rooted.Open(MetadataDB)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotCalibre, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("calibre: stat %s: %w", MetadataDB, err)
	}

	db, err := sql.Open("sqlite", readOnlyDSN(filepath.Join(absolute, MetadataDB)))
	if err != nil {
		return nil, fmt.Errorf("calibre: open %s: %w", MetadataDB, err)
	}
	// One connection. A refresh reads this database in a single
	// sequential pass, and a pool would only multiply the file handles
	// held open on somebody else's library.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("calibre: read %s: %w", MetadataDB, err)
	}
	if err := sameFileAsOpened(db, info); err != nil {
		db.Close()
		return nil, err
	}
	return &Library{root: absolute, db: db}, nil
}

// Root is the library directory, absolute.
func (l *Library) Root() string { return l.root }

// Missing lists the relations this database did not have, in the order
// they were looked for. It is empty for a Calibre library of the
// versions this was written against, and non-empty is worth reporting
// rather than hiding: it says which fields will be blank and why.
func (l *Library) Missing() []string { return l.missing }

func (l *Library) Close() error { return l.db.Close() }

// readOnlyDSN builds the driver URI. mode=ro cannot create the file and
// cannot write it; query_only(1) is the same statement made again
// inside the connection, so a bug here fails loudly rather than
// modifying a library we promised not to touch.
//
// The path is escaped as a URI path, which is what stops a directory
// name containing "?" or "#" from injecting driver parameters.
func readOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String() +
		"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
}

// sameFileAsOpened compares the database SQLite actually opened with the
// file that was checked at the root. PRAGMA database_list names the main
// database's path; os.SameFile does the comparison portably, which
// device and inode numbers would not.
func sameFileAsOpened(db *sql.DB, opened os.FileInfo) error {
	rows, err := db.Query(`PRAGMA database_list`)
	if err != nil {
		return fmt.Errorf("calibre: database_list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			return fmt.Errorf("calibre: database_list: %w", err)
		}
		if name != "main" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUnsafeRoot, err)
		}
		if !os.SameFile(info, opened) {
			return fmt.Errorf("%w: SQLite opened %q", ErrUnsafeRoot, path)
		}
		return rows.Err()
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("calibre: database_list: %w", err)
	}
	return fmt.Errorf("%w: SQLite reported no main database", ErrUnsafeRoot)
}

// noteMissing records a relation this database does not have. The same
// relation is only recorded once, however many books ask for it.
func (l *Library) noteMissing(relation string) {
	for _, seen := range l.missing {
		if seen == relation {
			return
		}
	}
	l.missing = append(l.missing, relation)
}

// isMissingRelation reports whether an error is SQLite saying the
// schema does not have what was asked for, as opposed to saying the
// database is unreadable. Calibre's schema is not ours and has moved
// between versions; a column that went away should cost the field it
// carried, not the refresh.
func isMissingRelation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "no such column")
}
