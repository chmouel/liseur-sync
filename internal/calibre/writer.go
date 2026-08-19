package calibre

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"modernc.org/sqlite"
)

// Writing into somebody's Calibre library (ADR-0023 phase 3).
//
// This is a separate type from Library on purpose. Library opens
// metadata.db read-only, with query_only(1), and says in a comment that
// a bug there should fail loudly rather than modify a library we
// promised not to touch. That promise is still kept: a pass cannot
// write, because a pass has no Writer. Only code that named itself one
// can, and only for a folder an administrator marked.
//
// The reason this exists at all is that a Calibre library is not a
// directory of EPUBs with a database in it. Discovery comes from
// metadata.db (ADR-0022), so a file copied into the tree is not a book
// there — it is litter the pass will not see and the curator will not
// see. Adding a book means adding a Calibre book.

// ErrLocked is the database refusing to be written because something
// else holds it.
//
// It is worth being honest about how far this goes. Calibre caches
// metadata.db in memory and writes its cache back, and an *idle*
// Calibre holds no lock at all — so this catches Calibre mid-write and
// nothing else. A book added while somebody has the library open in
// Calibre can still be lost when Calibre next saves.
//
// There is no fix for that on this side: Calibre offers no lock to take
// and no protocol to ask. The mitigation is the one the deployment
// already implies — a server's library is not also somebody's desktop
// Calibre session — and the answer here is a 409 telling the reader to
// close Calibre, not a pretence that the race was handled.
var ErrLocked = errors.New("calibre: the library is busy")

// ErrNoTitle is a publication with nothing to file it under. Calibre's
// own default is "Unknown", and using it silently would bury the book
// somewhere the reader will never look for it.
var ErrNoTitle = errors.New("calibre: the publication has no title")

// registerOnce installs the SQL functions Calibre's own triggers call.
//
// books_insert_trg runs `UPDATE books SET sort=title_sort(NEW.title),
// uuid=uuid4()`, and neither function is built into SQLite: Calibre
// registers them from Python on every connection it opens. A third-party
// insert without them fails with "no such function" — and, worse, passes
// against any test fixture that left the triggers out. So they are
// registered for the whole driver, which costs nothing anywhere else:
// no other database in this server has a trigger that calls them.
var registerOnce sync.Once

func registerCalibreFunctions() {
	registerOnce.Do(func() {
		_ = sqlite.RegisterDeterministicScalarFunction("title_sort", 1,
			func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				if len(args) != 1 {
					return nil, errors.New("title_sort takes one argument")
				}
				title, _ := args[0].(string)
				return TitleSort(title), nil
			})
		_ = sqlite.RegisterScalarFunction("uuid4", 0,
			func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
				return uuid.NewString(), nil
			})
	})
}

// leadingArticles are the words Calibre moves to the end of a sort
// title. Calibre's default is the English set and a configurable regex;
// this is that default, because a server has no user preference to read
// and a title Calibre would sort differently is a cosmetic disagreement
// rather than a lost book.
var leadingArticles = []string{"A ", "An ", "The "}

// TitleSort is Calibre's title_sort: a leading article moves to the end.
// "The Left Hand of Darkness" sorts as "Left Hand of Darkness, The".
func TitleSort(title string) string {
	trimmed := strings.TrimSpace(title)
	for _, article := range leadingArticles {
		if len(trimmed) > len(article) &&
			strings.EqualFold(trimmed[:len(article)], article) {
			return trimmed[len(article):] + ", " + strings.TrimSpace(article)
		}
	}
	return trimmed
}

// AuthorSort is Calibre's author_to_author_sort: "Ursula K. Le Guin"
// files under "Le Guin, Ursula K.". Calibre has a longer list of
// suffixes and particles; this handles the common shape and leaves the
// rest alone, which is what a curator would then correct by hand.
func AuthorSort(name string) string {
	fields := strings.Fields(strings.TrimSpace(name))
	if len(fields) < 2 {
		return strings.TrimSpace(name)
	}
	last := fields[len(fields)-1]
	rest := strings.Join(fields[:len(fields)-1], " ")
	return last + ", " + rest
}

// NewBook is one publication to add, as its own metadata describes it.
// Nothing here is inferred and nothing is looked up: this server does
// not edit metadata (ADR-0017), so what the file says is what the
// catalog gets.
type NewBook struct {
	Title       string
	Authors     []string
	Publisher   string
	Description string
	Languages   []string
	Tags        []string
	Series      string
	SeriesIndex float64
	Identifiers []Identifier
	Published   *time.Time
	// Cover is the publication's cover as JPEG bytes, or nil. A book
	// with no cover is a book Calibre draws a placeholder for, which is
	// better than a has_cover flag pointing at a file that is not there.
	Cover []byte
}

// Identifier is one publication identifier, in Calibre's own spelling:
// a lowercase type ("isbn", "google") and its value.
type Identifier struct {
	Type  string
	Value string
}

// Writer adds books to a Calibre library.
type Writer struct {
	db *sql.DB
	// root is the library root as a path, for the database DSN.
	root string
	// dir is the same root as a root: every file this type creates goes
	// through it, so a symlink cannot redirect a write — or a rollback's
	// delete — outside the library. The plain-folder path has always
	// worked this way and there is no reason a Calibre library deserves
	// less care than a folder of loose EPUBs.
	dir *os.Root
}

// OpenWriter opens a library for writing.
//
// It does not reuse Library's Open, because that one's whole contract is
// that it cannot write. What it does reuse is the safety: the root is
// opened as a root, metadata.db is refused if it is a symlink or not a
// regular file, and the database SQLite ends up with is compared against
// the file that was checked.
func OpenWriter(root string) (*Writer, error) {
	registerCalibreFunctions()
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("calibre: resolve root %q: %w", root, err)
	}
	rooted, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("calibre: open root %q: %w", absolute, err)
	}
	link, err := rooted.Lstat(MetadataDB)
	if err != nil {
		rooted.Close()
		return nil, fmt.Errorf("%w: %w", ErrNotCalibre, err)
	}
	if !link.Mode().IsRegular() {
		rooted.Close()
		return nil, fmt.Errorf("%w: %s is not a regular file",
			ErrNotCalibre, MetadataDB)
	}
	file, err := rooted.Open(MetadataDB)
	if err != nil {
		rooted.Close()
		return nil, fmt.Errorf("%w: %w", ErrNotCalibre, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		rooted.Close()
		return nil, fmt.Errorf("calibre: stat %s: %w", MetadataDB, err)
	}
	db, err := sql.Open("sqlite",
		writeDSN(filepath.Join(absolute, MetadataDB)))
	if err != nil {
		rooted.Close()
		return nil, fmt.Errorf("calibre: open %s: %w", MetadataDB, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		rooted.Close()
		return nil, fmt.Errorf("calibre: open %s for writing: %w", MetadataDB, err)
	}
	if err := sameFileAsOpened(db, info); err != nil {
		db.Close()
		rooted.Close()
		return nil, err
	}
	return &Writer{db: db, root: absolute, dir: rooted}, nil
}

// Close releases the database and the root. Calling it twice is safe,
// which matters because the tests do exactly that.
func (w *Writer) Close() error {
	err := w.db.Close()
	if w.dir != nil {
		w.dir.Close()
		w.dir = nil
	}
	return err
}

// writeDSN is the read-only DSN's counterpart. The busy timeout is how
// long this server waits for another writer to let go before giving up;
// see ErrLocked for what that does and does not protect against.
func writeDSN(pathname string) string {
	u := url.URL{Scheme: "file", Path: pathname}
	return u.String() +
		"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(0)"
}

// AddBook writes one publication into the library and answers with
// Calibre's book id and the publication's path relative to the root.
//
// It is all-or-nothing, and it has to be. ADR-0022 makes metadata.db
// authoritative here, so a file with no row is invisible forever and a
// row with no file is a book the pass keeps re-observing and cannot
// serve. On any failure the transaction rolls back and the book's
// directory is removed.
func (w *Writer) AddBook(
	ctx context.Context, book NewBook, src io.Reader, size int64,
) (int64, string, error) {
	title := strings.TrimSpace(book.Title)
	if title == "" {
		return 0, "", ErrNoTitle
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		if isLocked(err) {
			return 0, "", ErrLocked
		}
		return 0, "", fmt.Errorf("calibre: begin: %w", err)
	}
	committed := false
	var made madeDirs
	defer func() {
		if committed {
			return
		}
		_ = tx.Rollback()
		w.remove(made)
	}()

	id, relative, dir, err := w.insert(ctx, tx, title, book, size)
	if err != nil {
		if isLocked(err) {
			return 0, "", ErrLocked
		}
		return 0, "", err
	}
	made, err = w.writeFiles(book, dir, relative, src)
	if err != nil {
		return 0, "", err
	}
	if err := tx.Commit(); err != nil {
		if isLocked(err) {
			return 0, "", ErrLocked
		}
		return 0, "", fmt.Errorf("calibre: commit: %w", err)
	}
	committed = true
	return id, relative, nil
}

// insert writes every row. The order is forced by Calibre's schema: the
// books row has to exist before its id can go into a path or a link, and
// the fkc_* triggers refuse a link whose book is not there yet.
func (w *Writer) insert(
	ctx context.Context, tx *sql.Tx, title string, book NewBook, size int64,
) (id int64, relative, dir string, err error) {
	authors := cleanStrings(book.Authors)
	if len(authors) == 0 {
		authors = []string{"Unknown"}
	}
	now := time.Now().UTC()
	published := now
	if book.Published != nil {
		published = book.Published.UTC()
	}
	seriesIndex := book.SeriesIndex
	if seriesIndex == 0 {
		seriesIndex = 1
	}
	hasCover := 0
	if len(book.Cover) > 0 {
		hasCover = 1
	}

	// path is empty for now: it contains the id, and the id does not
	// exist until this insert has run. Calibre-web has the same problem
	// and solves it the same way.
	res, err := tx.ExecContext(ctx,
		`INSERT INTO books
		     (title, timestamp, pubdate, series_index, author_sort, path,
		      has_cover, last_modified)
		 VALUES (?, ?, ?, ?, ?, '', ?, ?)`,
		title, calibreTime(now), calibreTime(published), seriesIndex,
		AuthorSort(authors[0]), hasCover, calibreTime(now))
	if err != nil {
		return 0, "", "", fmt.Errorf("calibre: insert book: %w", err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, "", "", fmt.Errorf("calibre: book id: %w", err)
	}

	dir = path.Join(
		FilenameSafe(authors[0], 96),
		FilenameSafe(title, 96)+" ("+strconv.FormatInt(id, 10)+")")
	name := FilenameSafe(title, 42) + " - " + FilenameSafe(authors[0], 42)
	relative = path.Join(dir, name+".epub")

	if _, err := tx.ExecContext(ctx,
		`UPDATE books SET path = ? WHERE id = ?`, dir, id); err != nil {
		return 0, "", dir, fmt.Errorf("calibre: set path: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO data (book, format, uncompressed_size, name)
		 VALUES (?, 'EPUB', ?, ?)`, id, size, name); err != nil {
		return 0, "", dir, fmt.Errorf("calibre: insert format: %w", err)
	}
	for _, author := range authors {
		authorID, err := upsertNamed(ctx, tx,
			"authors", "name", author, AuthorSort(author))
		if err != nil {
			return 0, "", dir, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO books_authors_link (book, author) VALUES (?, ?)`,
			id, authorID); err != nil {
			return 0, "", dir, fmt.Errorf("calibre: link author: %w", err)
		}
	}
	if err := w.insertRelations(ctx, tx, id, book); err != nil {
		return 0, "", dir, err
	}
	return id, relative, dir, nil
}

// insertRelations writes everything a book can have and often does not.
// Each is skipped when empty rather than written blank, because Calibre
// treats an empty publisher as a publisher.
func (w *Writer) insertRelations(
	ctx context.Context, tx *sql.Tx, id int64, book NewBook,
) error {
	if description := strings.TrimSpace(book.Description); description != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO comments (book, text) VALUES (?, ?)`,
			id, description); err != nil {
			return fmt.Errorf("calibre: insert description: %w", err)
		}
	}
	if publisher := strings.TrimSpace(book.Publisher); publisher != "" {
		publisherID, err := upsertNamed(ctx, tx, "publishers", "name", publisher, "")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO books_publishers_link (book, publisher) VALUES (?, ?)`,
			id, publisherID); err != nil {
			return fmt.Errorf("calibre: link publisher: %w", err)
		}
	}
	if series := strings.TrimSpace(book.Series); series != "" {
		seriesID, err := upsertNamed(ctx, tx, "series", "name", series, "")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO books_series_link (book, series) VALUES (?, ?)`,
			id, seriesID); err != nil {
			return fmt.Errorf("calibre: link series: %w", err)
		}
	}
	for _, tag := range cleanStrings(book.Tags) {
		tagID, err := upsertNamed(ctx, tx, "tags", "name", tag, "")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO books_tags_link (book, tag) VALUES (?, ?)`,
			id, tagID); err != nil {
			return fmt.Errorf("calibre: link tag: %w", err)
		}
	}
	for order, code := range cleanStrings(book.Languages) {
		langID, err := upsertNamed(ctx, tx, "languages", "lang_code", code, "")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO books_languages_link (book, lang_code, item_order)
			 VALUES (?, ?, ?)`, id, langID, order); err != nil {
			return fmt.Errorf("calibre: link language: %w", err)
		}
	}
	for _, ident := range book.Identifiers {
		kind := strings.ToLower(strings.TrimSpace(ident.Type))
		value := strings.TrimSpace(ident.Value)
		if kind == "" || value == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO identifiers (book, type, val)
			 VALUES (?, ?, ?)`, id, kind, value); err != nil {
			return fmt.Errorf("calibre: insert identifier: %w", err)
		}
	}
	return nil
}

// upsertNamed finds or creates one row in a Calibre lookup table. The
// UNIQUE(name) constraint is what makes this a lookup rather than a
// duplicate: two books by one author share the author's row, which is
// exactly what Calibre's own author page is counting.
func upsertNamed(
	ctx context.Context, tx *sql.Tx, table, column, value, sortAs string,
) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM `+table+` WHERE `+column+` = ?`, value).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("calibre: read %s: %w", table, err)
	}
	var res sql.Result
	if sortAs != "" {
		res, err = tx.ExecContext(ctx,
			`INSERT INTO `+table+` (`+column+`, sort) VALUES (?, ?)`,
			value, sortAs)
	} else {
		res, err = tx.ExecContext(ctx,
			`INSERT INTO `+table+` (`+column+`) VALUES (?)`, value)
	}
	if err != nil {
		return 0, fmt.Errorf("calibre: insert %s: %w", table, err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("calibre: %s id: %w", table, err)
	}
	return id, nil
}

// writeFiles creates the book's directory and puts the publication, the
// cover and an OPF in it — the three things Calibre expects to find and
// this server's own Calibre reader looks for.
//
// It runs inside the transaction's lifetime so that a failure here still
// rolls the rows back. The directory is created with O_EXCL semantics by
// virtue of containing the book id, which no other book has.
func (w *Writer) writeFiles(
	book NewBook, dir, relative string, src io.Reader,
) (made madeDirs, err error) {
	author := path.Dir(dir)
	// The author's directory is shared: two books by one writer live
	// under it, so finding it already there is the normal case and not
	// a collision. The book's directory is not shared — it carries the
	// book id — so finding *that* there means something this code did
	// not create is in the way, and writing into it would overwrite
	// somebody's book.
	switch err := w.dir.Mkdir(author, 0o755); {
	case err == nil:
		made.author = author
	case errors.Is(err, os.ErrExist):
	default:
		return made, fmt.Errorf("calibre: create author directory: %w", err)
	}
	if err := w.dir.Mkdir(dir, 0o755); err != nil {
		return made, fmt.Errorf("calibre: create book directory: %w", err)
	}
	made.book = dir

	if err := w.create(relative, func(f *os.File) error {
		if _, err := io.Copy(f, src); err != nil {
			return err
		}
		return f.Sync()
	}); err != nil {
		return made, fmt.Errorf("calibre: write publication: %w", err)
	}
	if len(book.Cover) > 0 {
		if err := w.create(path.Join(dir, CoverName), func(f *os.File) error {
			_, err := f.Write(book.Cover)
			return err
		}); err != nil {
			return made, fmt.Errorf("calibre: write cover: %w", err)
		}
	}
	if err := w.create(path.Join(dir, OPFName), func(f *os.File) error {
		_, err := io.WriteString(f, book.opf())
		return err
	}); err != nil {
		return made, fmt.Errorf("calibre: write metadata.opf: %w", err)
	}
	return made, nil
}

// create makes one file that was not there. O_EXCL and O_NOFOLLOW are
// both load-bearing: the first is what makes this an addition rather
// than an overwrite, and the second stops a symlink somebody left in
// the library from turning a write into a write somewhere else.
func (w *Writer) create(name string, write func(*os.File) error) error {
	file, err := w.dir.OpenFile(name,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if err := write(file); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// madeDirs is what a failed add has to undo. Only directories this code
// created are in it, so a rollback can never remove a directory that
// was already somebody's.
type madeDirs struct {
	author string
	book   string
}

// remove undoes the directories, innermost first. The book's directory
// holds only files this code just wrote, and the author's is removed
// with Remove rather than RemoveAll — which refuses a non-empty
// directory, so an author who already had books keeps them.
func (w *Writer) remove(made madeDirs) {
	if made.book != "" {
		_ = w.dir.RemoveAll(made.book)
	}
	if made.author != "" {
		_ = w.dir.Remove(made.author)
	}
}

// calibreTime is the timestamp format Calibre stores and this package
// already parses on the way back in.
func calibreTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000000-07:00")
}

// isLocked recognises the one failure worth its own answer: something
// else is writing the database right now.
func isLocked(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// FilenameSafe is Calibre's own filename rule, which matters because
// Calibre and calibre-web both re-derive these names and a name they
// would spell differently is a name they will rename.
//
// A trailing dot becomes an underscore, the characters Windows refuses
// become underscores, a pipe becomes a comma, and the result is cut to a
// byte budget on a rune boundary.
func FilenameSafe(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".")
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '|':
			b.WriteRune(',')
		case '*', '+', ':', '\\', '"', '/', '<', '>', '?', 0:
			b.WriteRune('_')
		default:
			if r < 0x20 {
				continue
			}
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > maxBytes {
		out = strings.ToValidUTF8(out[:maxBytes], "")
		out = strings.TrimSpace(out)
	}
	if out == "" {
		return "Unknown"
	}
	return out
}

// ErrNoSuchBook is a book id metadata.db does not have. Deleting one
// that is already gone is the caller's business to forgive, not this
// package's to hide.
var ErrNoSuchBook = errors.New("calibre: no such book")

// DeleteBook removes one book from the library: its row, everything the
// schema hangs off that row, and the directory holding its files
// (ADR-0025).
//
// The cascade is Calibre's own. books_delete_trg deletes the author,
// publisher, rating, series, tag and language links, the formats, the
// comments, the identifiers, the annotations, the read positions, the
// conversion options and the plugin data. Enumerating those here would
// be a second copy of the schema to drift out of date, so this deletes
// the row and lets the trigger do what it exists to do. What the
// trigger does not do is prune the author, tag or series rows it
// unlinked: Calibre leaves those to its own clean pass, and so does
// this — pruning a shared row is a library-wide decision.
//
// The directory comes from books.path read inside the transaction, not
// from anything a caller cached. Calibre renames a book's directory when
// its title or author changes, so a path from an earlier pass may name a
// directory that has since moved — or one Calibre has since given to a
// different book.
//
// This is the one delete in the server that removes the file after the
// row rather than before, because here it can: metadata.db is a
// transaction, so a failed removal rolls the row back rather than
// leaving a library whose files have no book.
func (w *Writer) DeleteBook(ctx context.Context, id int64) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		if isLocked(err) {
			return ErrLocked
		}
		return fmt.Errorf("calibre: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var dir string
	switch err := tx.QueryRowContext(ctx,
		`SELECT path FROM books WHERE id = ?`, id).Scan(&dir); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoSuchBook
	case isLocked(err):
		return ErrLocked
	case err != nil:
		return fmt.Errorf("calibre: read book path: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM books WHERE id = ?`, id); err != nil {
		if isLocked(err) {
			return ErrLocked
		}
		return fmt.Errorf("calibre: delete book: %w", err)
	}
	if err := w.removeBookDir(dir); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		if isLocked(err) {
			return ErrLocked
		}
		return fmt.Errorf("calibre: commit: %w", err)
	}
	committed = true
	return nil
}

// removeBookDir removes a book's directory and, if that empties it, the
// author's above it. Rooted, and refusing anything that is not a plain
// directory: a symlink where a book's directory should be is not a book
// this server put there, and following it would delete somewhere else.
func (w *Writer) removeBookDir(dir string) error {
	dir = path.Clean(dir)
	if dir == "" || dir == "." || dir == "/" || strings.HasPrefix(dir, "../") {
		return fmt.Errorf("calibre: refusing to remove %q", dir)
	}
	info, err := w.dir.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		// Already gone. The row is what the library reads, and it is
		// about to go too.
		return nil
	}
	if err != nil {
		return fmt.Errorf("calibre: stat book directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("calibre: %q is not a directory", dir)
	}
	if err := w.dir.RemoveAll(dir); err != nil {
		return fmt.Errorf("calibre: remove book directory: %w", err)
	}
	// Remove, not RemoveAll: an author with other books keeps them.
	if author := path.Dir(dir); author != "." && author != "/" {
		_ = w.dir.Remove(author)
	}
	return nil
}
