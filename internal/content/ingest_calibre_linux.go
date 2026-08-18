package content

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Uploading into a Calibre library, ADR-0023 phase 3.
//
// A Calibre folder is not a directory of EPUBs. Discovery there comes
// from metadata.db (ADR-0022), so copying a file into the tree produces
// something the pass will not catalog and the curator will not see —
// litter under somebody's library. Adding a book means adding a Calibre
// book: rows, the directory layout Calibre names, a cover and an OPF.
//
// The write is the narrowest one that produces a book Calibre and this
// server both read back. It never touches a row that was already there,
// beyond reusing an author or a tag by name, which is what sharing an
// author is.

// placeCalibre adds one publication to a Calibre library and answers
// with its path relative to the folder root.
func placeCalibre(
	ctx context.Context, folder store.Folder, up Upload,
) (string, error) {
	if !folder.AcceptsUploads {
		return "", ErrUploadsRefused
	}
	writer, err := calibre.OpenWriter(folder.RootPath)
	if err != nil {
		return "", fmt.Errorf("content: open Calibre library: %w", err)
	}
	defer func() { _ = writer.Close() }()

	book := calibreBookFrom(up)
	_, relative, err := writer.AddBook(ctx, book, up.Source, up.Size)
	if err != nil {
		return "", err
	}
	return relative, nil
}

// calibreBookFrom translates a publication's own metadata into the
// columns Calibre keeps. Nothing is invented: a field the EPUB did not
// declare is left out rather than guessed, because this server does not
// edit metadata and a wrong publisher is worse than none.
func calibreBookFrom(up Upload) calibre.NewBook {
	meta := up.Meta
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		title = strings.TrimSpace(up.Base)
	}
	book := calibre.NewBook{
		Title:       title,
		Authors:     authorsOf(meta),
		Publisher:   meta.Publisher,
		Description: meta.Description,
		Languages:   meta.Languages,
		Tags:        meta.Subjects,
		Published:   publishedAt(meta.PublishedDate),
		Cover:       up.Cover,
	}
	if len(meta.Series) > 0 {
		book.Series = meta.Series[0].Name
		if position := meta.Series[0].Position; position != nil {
			book.SeriesIndex = *position
		}
	}
	for _, ident := range meta.Identifiers {
		scheme := strings.ToLower(strings.TrimSpace(ident.Scheme))
		if scheme == "" {
			scheme = "unknown"
		}
		book.Identifiers = append(book.Identifiers,
			calibre.Identifier{Type: scheme, Value: ident.Value})
	}
	return book
}

// authorsOf keeps the contributors Calibre would call authors, and falls
// back to every contributor when a publication used no roles at all.
func authorsOf(meta epub.Metadata) []string {
	var authors []string
	for _, c := range meta.Contributors {
		if c.Role == "aut" || c.Role == "author" {
			authors = append(authors, c.Name)
		}
	}
	if len(authors) > 0 {
		return authors
	}
	for _, c := range meta.Contributors {
		authors = append(authors, c.Name)
	}
	return authors
}

// publishedAt reads the several shapes a dc:date takes. A date that
// parses as none of them is dropped: Calibre's own default is today,
// and a wrong publication year is a book filed under the wrong decade.
func publishedAt(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{
		time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02", "2006-01", "2006",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return &parsed
		}
	}
	if year, err := strconv.Atoi(value); err == nil && year > 0 && year < 3000 {
		parsed := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
		return &parsed
	}
	return nil
}
