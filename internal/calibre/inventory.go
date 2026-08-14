package calibre

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"
)

// Inventory is what a refresh compares against the last one to decide
// whether there is anything to do.
//
// It is a full read rather than a stat of metadata.db, and that is not
// caution for its own sake: Calibre runs SQLite in WAL mode, so a
// commit can leave the main database file untouched while the change
// sits in metadata.db-wal. A stat-gated refresh would miss edits and
// deletions indefinitely. Counting rows and taking a maximum
// last_modified fails more subtly — a book edited at the current
// maximum moves neither, and neither does one row replaced by another.
//
// So the gate is every book's id and modification time, its formats,
// and the size and modification time of each file, hashed into one
// digest. That is an index scan and a stat per book: cheap next to
// opening a single EPUB, and cheap enough to run every interval.
type Inventory struct {
	// Digest is the whole inventory in one value. Equal digests mean
	// the refresh has nothing to do.
	Digest string
	// Books is what the digest was computed from, in id order, and is
	// also what drives the work when the digest has moved: per-book
	// timestamps select the rows to re-read, and the id set is what
	// reconciles deletions, which no timestamp can.
	Books []InventoryBook
}

// InventoryBook is one book as the gate sees it.
type InventoryBook struct {
	ID           int64
	LastModified time.Time
	// Files are the book's formats and its cover, as they are on the
	// disk right now.
	Files []FileStamp
}

// FileStamp is one file's identity as far as a cheap check can tell:
// where it is, how big it is, and when it was last written.
//
// It detects every change that moves a file's size or modification
// time, which is not the same as every change. A file replaced with
// different bytes of the same size and the same mtime is invisible
// here, exactly as it is to the in-place read check, and for the same
// reason: the alternative is hashing every file in the library on every
// interval.
type FileStamp struct {
	RelativePath string
	// Present is false for a file Calibre lists that is not on the
	// disk. Its absence is part of the inventory, because a file coming
	// back is a change too.
	Present    bool
	SizeBytes  int64
	ModifiedAt time.Time
}

// Inventory reads the whole library's change-detection state.
func (l *Library) Inventory(ctx context.Context) (Inventory, error) {
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return Inventory{}, fmt.Errorf("calibre: open root: %w", err)
	}
	defer root.Close()

	tx, err := l.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Inventory{}, fmt.Errorf("calibre: begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	books, order, err := l.readBooks(ctx, tx)
	if err != nil {
		return Inventory{}, err
	}
	if err := l.readFormats(ctx, tx, books); err != nil {
		return Inventory{}, err
	}

	inventory := Inventory{Books: make([]InventoryBook, 0, len(order))}
	digest := sha256.New()
	for _, id := range order {
		if err := ctx.Err(); err != nil {
			return Inventory{}, err
		}
		book := books[id]
		entry := InventoryBook{ID: book.ID, LastModified: book.LastModified}
		paths := make([]string, 0, len(book.Formats)+1)
		for _, f := range book.Formats {
			paths = append(paths, book.RelativePath(f))
		}
		if book.HasCover {
			paths = append(paths, book.CoverPath())
		}
		// Sorted so that two reads of an unchanged library hash the
		// same bytes whatever order the rows came back in.
		sort.Strings(paths)
		for _, rel := range paths {
			entry.Files = append(entry.Files, stat(root, rel))
		}
		writeInventory(digest, entry)
		inventory.Books = append(inventory.Books, entry)
	}
	inventory.Digest = hex.EncodeToString(digest.Sum(nil))
	return inventory, nil
}

// stat looks at one file through the rooted directory, so a path
// Calibre stored that climbs out of the library is refused by the same
// mechanism that refuses one in an EPUB.
func stat(root *os.Root, rel string) FileStamp {
	stamp := FileStamp{RelativePath: rel}
	info, err := root.Stat(rel)
	if err != nil {
		// Anything that is not "it is there and it is a file" is
		// absence: a permission error and a deletion are the same fact
		// to a server that cannot read the bytes either way.
		return stamp
	}
	if !info.Mode().IsRegular() {
		return stamp
	}
	stamp.Present = true
	stamp.SizeBytes = info.Size()
	// Truncated to the second, which is the resolution every filesystem
	// this could run on agrees about.
	stamp.ModifiedAt = info.ModTime().UTC().Truncate(time.Second)
	return stamp
}

// writeInventory feeds one book into the digest. Field separators are
// explicit so that two different inventories cannot hash to the same
// bytes by running fields together.
func writeInventory(h interface{ Write([]byte) (int, error) }, b InventoryBook) {
	write := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	write(strconv.FormatInt(b.ID, 10))
	write(strconv.FormatInt(b.LastModified.UTC().Unix(), 10))
	for _, f := range b.Files {
		write(f.RelativePath)
		if !f.Present {
			write("-")
			continue
		}
		write(strconv.FormatInt(f.SizeBytes, 10))
		write(strconv.FormatInt(f.ModifiedAt.Unix(), 10))
	}
}

// Changed reports whether this inventory differs from the digest a
// previous refresh recorded. An empty previous digest is a library that
// has never been refreshed, which has changed by definition.
func (i Inventory) Changed(previousDigest string) bool {
	return previousDigest == "" || previousDigest != i.Digest
}

// IDs is the set of Calibre book ids this inventory found, which is
// what a refresh reconciles deletions against.
func (i Inventory) IDs() map[int64]struct{} {
	out := make(map[int64]struct{}, len(i.Books))
	for _, b := range i.Books {
		out[b.ID] = struct{}{}
	}
	return out
}
