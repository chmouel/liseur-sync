package webui_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestReaderPositionArithmetic runs the node unit tests for the footer's
// page number (static/reader-positions.js). The page is a Readium
// position, counted the same way the Android app counts it, so that the
// two clients name the same page for the same spot; the module is pure
// arithmetic over a book's sections precisely so that agreement can be
// checked without a browser.
func TestReaderPositionArithmetic(t *testing.T) {
	runNodeTests(t, "readerpositions.test.mjs")
}

// TestReaderDecodesNonUTF8PublicationText runs the node unit tests for
// reader-publication.js's decodeText: BOM and bare UTF-16, an XML
// declaration or CSS @charset naming another encoding, and the UTF-8
// fallback for everything else.
func TestReaderDecodesNonUTF8PublicationText(t *testing.T) {
	runNodeTests(t, "readerdecode.test.mjs")
}

// TestReaderPublicationCacheIsBounded runs the node unit tests for
// ReaderPublication's raw/blob cache: retain() releases entries whose
// referrers have all left Readium's active frame window, keeps a shared
// asset still referenced from inside that window, and never releases the
// package document.
func TestReaderPublicationCacheIsBounded(t *testing.T) {
	runNodeTests(t, "readerpublication.test.mjs")
}

// runNodeTests runs one `node --test` file from testdata. These are the
// reader checks that run in CI: the browser check needs a Chromium and
// skips without one.
func runNodeTests(t *testing.T, file string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("no node to run %s with", file)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--test", filepath.Join("testdata", file))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", file, err, out)
	}
}
