package calibre

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Book is one row of Calibre's books table, with the relations that
// describe it resolved.
//
// Everything here is what Calibre says. Nothing is inferred from the
// tree, and nothing is filled in from the EPUB: that is the precedence
// engine's job, one layer up, where a value from Calibre outranks a
// value from a file but loses to a value a human typed.
type Book struct {
	// ID is Calibre's own book id, and is the only identity this
	// package deals in. It is what a catalog row is mapped to, because
	// a digest match would merge two deliberately distinct books
	// (ADR-0014).
	ID int64
	// Path is the book's directory, relative to the library root, in
	// slash form as Calibre stores it.
	Path         string
	Title        string
	SortTitle    string
	Authors      []string
	Series       string
	SeriesIndex  float64
	Publisher    string
	Published    *time.Time
	Added        time.Time
	LastModified time.Time
	Description  string
	Tags         []string
	Languages    []string
	// Identifiers are Calibre's, keyed by its type — isbn, google,
	// amazon and whatever else the curator has added.
	Identifiers map[string]string
	// HasCover is Calibre's flag, not a stat: a true here means Calibre
	// believes it wrote cover.jpg, and the file may still be gone.
	HasCover bool
	// Formats are the files Calibre knows this book has, in the order
	// Calibre lists them.
	Formats []Format
}

// Format is one publication file of one book.
type Format struct {
	// Format is Calibre's uppercase format name: EPUB, MOBI, PDF.
	Format string
	// Name is the filename without its extension, which is how Calibre
	// stores it.
	Name string
	// SizeBytes is what Calibre recorded when it added the file, which
	// is not a promise about what is on the disk now.
	SizeBytes int64
}

// RelativePath is where this format's file lives under the library
// root. Calibre's own layout — <book path>/<name>.<lowercase format> —
// is what makes a Calibre library readable without walking it.
func (b Book) RelativePath(f Format) string {
	return path.Join(b.Path, f.Name+"."+strings.ToLower(f.Format))
}

// CoverPath is where this book's cover lives, whether or not it is
// there.
func (b Book) CoverPath() string { return path.Join(b.Path, CoverName) }

// EPUB returns the book's EPUB format, if it has one. A book with no
// EPUB is still a book — it is in the catalog and unavailable — so the
// caller decides what to do rather than being handed a zero value that
// looks like a file.
func (b Book) EPUB() (Format, bool) {
	for _, f := range b.Formats {
		if strings.EqualFold(f.Format, "EPUB") {
			return f, true
		}
	}
	return Format{}, false
}

// Books reads the whole library.
//
// It is one pass of a handful of queries rather than a query per book:
// a Calibre library of any size has a few thousand rows in each of
// these tables, and the alternative is a round trip per book per
// relation. Everything happens inside one read transaction, so the
// relations cannot disagree with the book list they describe.
func (l *Library) Books(ctx context.Context) ([]Book, error) {
	tx, err := l.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("calibre: begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	books, order, err := l.readBooks(ctx, tx)
	if err != nil {
		return nil, err
	}
	// Each of these is optional in the sense that a Calibre version
	// that does not have it costs the field, not the read. The book
	// list and the format list are not: without them there is nothing
	// to catalog.
	if err := l.readFormats(ctx, tx, books); err != nil {
		return nil, err
	}
	for _, read := range []func(context.Context, *sql.Tx, map[int64]*Book) error{
		l.readAuthors, l.readSeries, l.readPublishers, l.readTags,
		l.readLanguages, l.readIdentifiers, l.readComments,
	} {
		if err := read(ctx, tx, books); err != nil {
			return nil, err
		}
	}
	out := make([]Book, 0, len(order))
	for _, id := range order {
		out = append(out, *books[id])
	}
	return out, nil
}

// readBooks is the spine: every book Calibre knows about, in id order,
// which is also the order they were added.
func (l *Library) readBooks(
	ctx context.Context, tx *sql.Tx,
) (map[int64]*Book, []int64, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, title, sort, series_index, path, has_cover,
		        pubdate, timestamp, last_modified
		   FROM books
		  ORDER BY id`)
	if err != nil {
		if isMissingRelation(err) {
			// Without books there is no library to read, which is a
			// different failure from a version that spells a field
			// differently: it is reported as the schema being one this
			// server does not understand.
			return nil, nil, fmt.Errorf("%w: %w", ErrUnsupportedSchema, err)
		}
		return nil, nil, fmt.Errorf("calibre: read books: %w", err)
	}
	defer rows.Close()
	books := map[int64]*Book{}
	var order []int64
	for rows.Next() {
		var b Book
		var sortTitle, bookPath sql.NullString
		var seriesIndex sql.NullFloat64
		var hasCover sql.NullBool
		var pubdate, added, modified sql.NullString
		if err := rows.Scan(&b.ID, &b.Title, &sortTitle, &seriesIndex,
			&bookPath, &hasCover, &pubdate, &added, &modified); err != nil {
			return nil, nil, fmt.Errorf("calibre: read books: %w", err)
		}
		b.SortTitle = sortTitle.String
		b.SeriesIndex = seriesIndex.Float64
		b.Path = path.Clean(strings.ReplaceAll(bookPath.String, "\\", "/"))
		if b.Path == "." {
			b.Path = ""
		}
		b.HasCover = hasCover.Bool
		b.Published = parseNullTime(pubdate)
		if t := parseNullTime(added); t != nil {
			b.Added = *t
		}
		if t := parseNullTime(modified); t != nil {
			b.LastModified = *t
		}
		books[b.ID] = &b
		order = append(order, b.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("calibre: read books: %w", err)
	}
	return books, order, nil
}

func (l *Library) readFormats(
	ctx context.Context, tx *sql.Tx, books map[int64]*Book,
) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT book, format, name, uncompressed_size
		   FROM data ORDER BY book, format`)
	if err != nil {
		if isMissingRelation(err) {
			return fmt.Errorf("%w: %w", ErrUnsupportedSchema, err)
		}
		return fmt.Errorf("calibre: read formats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var book int64
		var f Format
		var size sql.NullInt64
		if err := rows.Scan(&book, &f.Format, &f.Name, &size); err != nil {
			return fmt.Errorf("calibre: read formats: %w", err)
		}
		f.SizeBytes = size.Int64
		if b, ok := books[book]; ok {
			b.Formats = append(b.Formats, f)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("calibre: read formats: %w", err)
	}
	return nil
}

// readLinked reads one of Calibre's many-to-many relations: a link
// table joined to a name table. They differ only in their table and
// column names, so they are one function rather than six copies with a
// typo in the fifth.
func (l *Library) readLinked(
	ctx context.Context, tx *sql.Tx, books map[int64]*Book,
	relation, query string, apply func(*Book, string),
) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		if isMissingRelation(err) {
			l.noteMissing(relation)
			return nil
		}
		return fmt.Errorf("calibre: read %s: %w", relation, err)
	}
	defer rows.Close()
	for rows.Next() {
		var book int64
		var value sql.NullString
		if err := rows.Scan(&book, &value); err != nil {
			return fmt.Errorf("calibre: read %s: %w", relation, err)
		}
		text := strings.TrimSpace(value.String)
		if text == "" {
			continue
		}
		if b, ok := books[book]; ok {
			apply(b, text)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("calibre: read %s: %w", relation, err)
	}
	return nil
}

func (l *Library) readAuthors(
	ctx context.Context, tx *sql.Tx, books map[int64]*Book,
) error {
	// Calibre's link table carries the author order, which is the
	// difference between "Gaiman and Pratchett" and "Pratchett and
	// Gaiman" — not a detail to a reader looking for a book.
	return l.readLinked(ctx, tx, books, "authors",
		`SELECT l.book, a.name
		   FROM books_authors_link l
		   JOIN authors a ON a.id = l.author
		  ORDER BY l.book, l.id`,
		func(b *Book, name string) {
			b.Authors = append(b.Authors, strings.ReplaceAll(name, "|", ","))
		})
}

func (l *Library) readSeries(
	ctx context.Context, tx *sql.Tx, books map[int64]*Book,
) error {
	// A book belongs to at most one series in Calibre; the last one
	// wins if a hand-edited database says otherwise.
	return l.readLinked(ctx, tx, books, "series",
		`SELECT l.book, s.name
		   FROM books_series_link l
		   JOIN series s ON s.id = l.series
		  ORDER BY l.book, l.id`,
		func(b *Book, name string) { b.Series = name })
}

func (l *Library) readPublishers(
	ctx context.Context, tx *sql.Tx, books map[int64]*Book,
) error {
	return l.readLinked(ctx, tx, books, "publishers",
		`SELECT l.book, p.name
		   FROM books_publishers_link l
		   JOIN publishers p ON p.id = l.publisher
		  ORDER BY l.book, l.id`,
		func(b *Book, name string) { b.Publisher = name })
}

func (l *Library) readTags(
	ctx context.Context, tx *sql.Tx, books map[int64]*Book,
) error {
	return l.readLinked(ctx, tx, books, "tags",
		`SELECT l.book, t.name
		   FROM books_tags_link l
		   JOIN tags t ON t.id = l.tag
		  ORDER BY l.book, t.name`,
		func(b *Book, name string) { b.Tags = append(b.Tags, name) })
}

func (l *Library) readLanguages(
	ctx context.Context, tx *sql.Tx, books map[int64]*Book,
) error {
	// languages is one of the tables that has not always existed, which
	// is exactly the case readLinked's missing-relation path is for.
	return l.readLinked(ctx, tx, books, "languages",
		`SELECT l.book, c.lang_code
		   FROM books_languages_link l
		   JOIN languages c ON c.id = l.lang_code
		  ORDER BY l.book, l.item_order`,
		func(b *Book, code string) { b.Languages = append(b.Languages, code) })
}

func (l *Library) readComments(
	ctx context.Context, tx *sql.Tx, books map[int64]*Book,
) error {
	return l.readLinked(ctx, tx, books, "comments",
		`SELECT book, text FROM comments ORDER BY book`,
		func(b *Book, text string) { b.Description = text })
}

func (l *Library) readIdentifiers(
	ctx context.Context, tx *sql.Tx, books map[int64]*Book,
) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT book, type, val FROM identifiers ORDER BY book, type`)
	if err != nil {
		if isMissingRelation(err) {
			l.noteMissing("identifiers")
			return nil
		}
		return fmt.Errorf("calibre: read identifiers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var book int64
		var kind, value sql.NullString
		if err := rows.Scan(&book, &kind, &value); err != nil {
			return fmt.Errorf("calibre: read identifiers: %w", err)
		}
		scheme := strings.ToLower(strings.TrimSpace(kind.String))
		text := strings.TrimSpace(value.String)
		if scheme == "" || text == "" {
			continue
		}
		b, ok := books[book]
		if !ok {
			continue
		}
		if b.Identifiers == nil {
			b.Identifiers = map[string]string{}
		}
		b.Identifiers[scheme] = text
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("calibre: read identifiers: %w", err)
	}
	return nil
}

// calibreTimeLayouts are the shapes Calibre's timestamps come in.
// It writes ISO-8601 with a numeric zone, but a database that has been
// through a few versions and a few tools holds all of these.
var calibreTimeLayouts = []string{
	"2006-01-02 15:04:05.999999-07:00",
	"2006-01-02 15:04:05.999999+00:00",
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// parseNullTime reads one of Calibre's timestamps, or nil for one that
// is absent or unparseable. An unreadable date is a missing date here:
// the alternative is failing a whole refresh over a field that only
// ever decorates a book page.
func parseNullTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	text := strings.TrimSpace(value.String)
	if text == "" {
		return nil
	}
	for _, layout := range calibreTimeLayouts {
		if t, err := time.Parse(layout, text); err == nil {
			// Calibre writes 0101-01-01 for "no date", which is its own
			// way of spelling nil.
			if t.Year() <= 1 {
				return nil
			}
			utc := t.UTC()
			return &utc
		}
	}
	return nil
}

// SortedTags is the tag list in a stable order, for callers that
// compare two reads of the same library.
func SortedTags(b Book) []string {
	out := append([]string(nil), b.Tags...)
	sort.Strings(out)
	return out
}
