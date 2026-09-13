//go:build linux

package content

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/store"
)

// placeBook writes a file under a root and answers the catalog row that
// would describe it, so the change gate has something true to check
// against.
func placeBook(t *testing.T, root, relative string, body []byte) store.CatalogBook {
	t.Helper()
	full := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	return store.CatalogBook{
		ID:           "book-1",
		RelativePath: relative,
		SizeBytes:    info.Size(),
		MTime:        info.ModTime().UTC(),
	}
}

func uploadFolder(root string) store.Folder {
	return store.Folder{
		ID: "f1", Kind: store.FolderPlain,
		RootPath: root, AcceptsUploads: true,
	}
}

func TestRemoveDeletesTheFileAndItsCover(t *testing.T) {
	root := t.TempDir()
	book := placeBook(t, root, "sub/book.epub", []byte("bytes"))
	cover := "sub/cover.jpg"
	if err := os.WriteFile(
		filepath.Join(root, cover), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	book.CoverRelativePath = &cover

	if err := Remove(t.Context(), uploadFolder(root), book); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{book.RelativePath, cover} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived: %v", name, err)
		}
	}
}

// The flag that bounds writing bounds unwriting. A folder nobody marked
// is still read-only to this server, and that is checked here rather
// than only at the handler edge.
func TestRemoveRefusesAFolderThatDoesNotAcceptUploads(t *testing.T) {
	root := t.TempDir()
	book := placeBook(t, root, "book.epub", []byte("bytes"))
	folder := uploadFolder(root)
	folder.AcceptsUploads = false

	if err := Remove(t.Context(), folder, book); !errors.Is(
		err, ErrUploadsRefused,
	) {
		t.Fatalf("err = %v, want ErrUploadsRefused", err)
	}
	if _, err := os.Stat(filepath.Join(root, book.RelativePath)); err != nil {
		t.Errorf("the file was deleted anyway: %v", err)
	}
}

// There is no trash behind this, so the difference between deleting the
// book and deleting whatever replaced it is the whole difference.
func TestRemoveRefusesAFileThatChangedSinceItWasScanned(t *testing.T) {
	root := t.TempDir()
	book := placeBook(t, root, "book.epub", []byte("bytes"))
	if err := os.WriteFile(
		filepath.Join(root, book.RelativePath),
		[]byte("a different book entirely"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(t.Context(), uploadFolder(root), book); !errors.Is(
		err, ErrRemoveChanged,
	) {
		t.Fatalf("err = %v, want ErrRemoveChanged", err)
	}
	if _, err := os.Stat(filepath.Join(root, book.RelativePath)); err != nil {
		t.Errorf("the replacement was deleted: %v", err)
	}
}

// A pass never records a symlink, so one at a catalog path is something
// the catalog never validated. Following it would delete outside the
// folder — the exact reason openUnderRoot refuses them on the way in.
func TestRemoveRefusesASymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "somebody-elses.epub")
	if err := os.WriteFile(outside, []byte("not yours"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "book.epub")); err != nil {
		t.Fatal(err)
	}
	book := store.CatalogBook{ID: "book-1", RelativePath: "book.epub"}

	if err := Remove(t.Context(), uploadFolder(root), book); !errors.Is(
		err, ErrUnsafePath,
	) {
		t.Fatalf("err = %v, want ErrUnsafePath", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the file the symlink pointed at was deleted: %v", err)
	}
}

func TestRemoveRefusesAPathThatEscapesTheRoot(t *testing.T) {
	root := t.TempDir()
	book := store.CatalogBook{ID: "book-1", RelativePath: "../escape.epub"}
	if err := Remove(t.Context(), uploadFolder(root), book); !errors.Is(
		err, ErrUnsafePath,
	) {
		t.Fatalf("err = %v, want ErrUnsafePath", err)
	}
}

// The caller's goal is that the file not be there, and it is not.
func TestRemoveForgivesAFileThatIsAlreadyGone(t *testing.T) {
	root := t.TempDir()
	book := placeBook(t, root, "book.epub", []byte("bytes"))
	if err := os.Remove(filepath.Join(root, book.RelativePath)); err != nil {
		t.Fatal(err)
	}
	if err := Remove(t.Context(), uploadFolder(root), book); err != nil {
		t.Fatalf("err = %v, want a delete of nothing to succeed", err)
	}
}

// A Calibre folder goes the other way round: the row is the book, and
// the directory comes from metadata.db rather than from the catalog
// row, which Calibre may have renamed out from under.
func TestRemoveInACalibreFolderTakesTheRowAndTheDirectory(t *testing.T) {
	root := writeCalibreLibrary(t)
	id := int64(1)
	book := store.CatalogBook{
		ID:           "book-1",
		RelativePath: "A Writer/Here (1)/Here.epub",
		CalibreID:    &id,
	}
	folder := store.Folder{
		ID: "f-calibre", Kind: store.FolderCalibre,
		RootPath: root, AcceptsUploads: true,
	}

	if err := Remove(t.Context(), folder, book); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(
		filepath.Join(root, "A Writer/Here (1)"),
	); !os.IsNotExist(err) {
		t.Errorf("the book directory survived: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, calibre.MetadataDB))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var books int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM books WHERE id = 1`).Scan(&books); err != nil {
		t.Fatal(err)
	}
	if books != 0 {
		t.Error("the row survived the delete")
	}
	// The other book's row is untouched. Its directory was never on the
	// disk in this fixture, which is why the author's went with the one
	// book that was; an author who still holds a book keeps it, and
	// that is asserted in internal/calibre.
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM books WHERE id = 2`).Scan(&books); err != nil {
		t.Fatal(err)
	}
	if books != 1 {
		t.Error("deleting one book took the other with it")
	}
}

// A Calibre row is what makes a Calibre book, so a catalog row without
// one names nothing this server can safely delete.
func TestRemoveRefusesACalibreBookWithNoCalibreID(t *testing.T) {
	root := writeCalibreLibrary(t)
	folder := store.Folder{
		ID: "f-calibre", Kind: store.FolderCalibre,
		RootPath: root, AcceptsUploads: true,
	}
	err := Remove(t.Context(), folder, store.CatalogBook{
		ID: "book-1", RelativePath: "A Writer/Here (1)/Here.epub",
	})
	if err == nil {
		t.Fatal("a Calibre book with no Calibre id was deleted anyway")
	}
}
