package calibre

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/chmouel/liseur-sync/internal/epub"
)

// MaxOPFBytes bounds a metadata.opf read. Calibre writes a few
// kilobytes; anything past this is not a metadata document, and this
// package reads files it does not own.
const MaxOPFBytes = 1 << 20

// ReadOPF fills the gaps in a book from the metadata.opf Calibre writes
// beside it.
//
// The database is the source of truth and this never overrides it, and
// "never overrides" is stricter than "fills what is empty". An empty
// value in a Calibre row is usually a deliberate clear — a description
// deleted, the last tag removed — and refilling it from an OPF Calibre
// wrote before the edit would resurrect it, and would keep resurrecting
// it on every refresh. So the fallback is keyed on the schema rather
// than on the value: it fills only fields whose relation this database
// does not have, which is the case the ADR is careful about — Calibre's
// schema is not ours, and a table that moved between versions should
// cost a field rather than a refresh.
//
// It therefore only has anything to say after the relations have been
// read: a Library that has not run Books or Inventory yet does not know
// what it is missing, and the OPF fills nothing.
//
// A missing or unreadable metadata.opf is not an error. The book keeps
// whatever the database gave it.
func (l *Library) ReadOPF(ctx context.Context, book *Book) error {
	if book == nil {
		return nil
	}
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return fmt.Errorf("calibre: open root: %w", err)
	}
	defer root.Close()
	return l.readOPFIn(ctx, root, book)
}

func (l *Library) readOPFIn(
	ctx context.Context, root *os.Root, book *Book,
) error {
	if book.Path == "" || len(l.missing) == 0 {
		return nil
	}
	rel := book.Path + "/" + OPFName
	document, err := readBounded(root, rel, MaxOPFBytes)
	if err != nil {
		return nil
	}
	parsed, err := epub.ParseMetadataDocument(ctx, document, epub.Limits{})
	if err != nil {
		return nil
	}
	mergeOPF(book, parsed, l.missingRelations())
	return nil
}

// missingRelations is Missing as a set, for the field-by-field question
// the OPF fallback asks.
func (l *Library) missingRelations() map[string]struct{} {
	out := make(map[string]struct{}, len(l.missing))
	for _, relation := range l.missing {
		out[relation] = struct{}{}
	}
	return out
}

// mergeOPF copies across only the fields this database has no relation
// for. A field the schema can express and left empty is Calibre saying
// it is empty, and is not this function's business.
//
// Title, sort title, the series index and the publication date are not
// here at all: they are columns of the books table, which every Calibre
// version has and without which there would be no refresh to fill.
func mergeOPF(book *Book, opf epub.Metadata, missing map[string]struct{}) {
	absent := func(relation string) bool {
		_, ok := missing[relation]
		return ok
	}
	if absent("comments") && book.Description == "" {
		book.Description = opf.Description
	}
	if absent("publishers") && book.Publisher == "" {
		book.Publisher = opf.Publisher
	}
	if absent("languages") && len(book.Languages) == 0 {
		book.Languages = append(book.Languages, opf.Languages...)
	}
	if absent("tags") && len(book.Tags) == 0 {
		book.Tags = append(book.Tags, opf.Subjects...)
	}
	if absent("authors") && len(book.Authors) == 0 {
		for _, contributor := range opf.Contributors {
			if contributor.Role != "" && contributor.Role != "aut" {
				continue
			}
			book.Authors = append(book.Authors, contributor.Name)
		}
	}
	if absent("series") && book.Series == "" && len(opf.Series) > 0 {
		book.Series = opf.Series[0].Name
		// The index is a column of books rather than of the series
		// relation, so it is only taken when the database had none.
		if position := opf.Series[0].Position; position != nil &&
			book.SeriesIndex == 0 {
			book.SeriesIndex = *position
		}
	}
	if absent("identifiers") && len(book.Identifiers) == 0 &&
		len(opf.Identifiers) > 0 {
		book.Identifiers = make(map[string]string, len(opf.Identifiers))
		for _, identifier := range opf.Identifiers {
			if identifier.Scheme == "" || identifier.Value == "" {
				continue
			}
			if _, ok := book.Identifiers[identifier.Scheme]; ok {
				continue
			}
			book.Identifiers[identifier.Scheme] = identifier.Value
		}
	}
}

// readBounded reads at most limit bytes of a file under the root,
// refusing anything that is not a regular file.
func readBounded(root *os.Root, rel string, limit int64) ([]byte, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("calibre: %s is not a regular file", rel)
	}
	file, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit))
}
