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
// The database is the source of truth and this never overrides it. It
// exists for the case the ADR is careful about: Calibre's schema is not
// ours, and a column that moved between versions leaves a field empty
// here that is present in the OPF, which Calibre keeps in a stable,
// specified format. Failing soft to the OPF for one book beats aborting
// a refresh of ten thousand.
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
	if book.Path == "" {
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
	mergeOPF(book, parsed)
	return nil
}

// mergeOPF copies across only the fields the database left empty.
func mergeOPF(book *Book, opf epub.Metadata) {
	if book.Title == "" {
		book.Title = opf.Title
	}
	if book.Description == "" {
		book.Description = opf.Description
	}
	if book.Publisher == "" {
		book.Publisher = opf.Publisher
	}
	if len(book.Languages) == 0 {
		book.Languages = append(book.Languages, opf.Languages...)
	}
	if len(book.Tags) == 0 {
		book.Tags = append(book.Tags, opf.Subjects...)
	}
	if len(book.Authors) == 0 {
		for _, contributor := range opf.Contributors {
			if contributor.Role != "" && contributor.Role != "aut" {
				continue
			}
			book.Authors = append(book.Authors, contributor.Name)
		}
	}
	if book.Series == "" && len(opf.Series) > 0 {
		book.Series = opf.Series[0].Name
		if position := opf.Series[0].Position; position != nil {
			book.SeriesIndex = *position
		}
	}
	if len(book.Identifiers) == 0 && len(opf.Identifiers) > 0 {
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
