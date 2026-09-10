package webui_test

import "testing"

func TestReaderLiveTransport(t *testing.T) {
	runNodeTests(t, "readerlive.test.mjs")
}

func TestReaderLiveState(t *testing.T) {
	runNodeTests(t, "readersync.test.mjs")
}

func TestReaderCredentialLifecycle(t *testing.T) {
	runNodeTests(t, "readerauth.test.mjs")
}

func TestReaderAnnotationReplacement(t *testing.T) {
	runNodeTests(t, "readerannotations.test.mjs")
}

func TestOfflinePublicationStorageCore(t *testing.T) {
	runNodeTests(t, "offline-storage.test.mjs")
}

// Three-way reconciliation is the whole of the reader's conflict
// behaviour, and it is pure: it deserves to be pinned on its own,
// away from a browser.
func TestReadingStateReconciliation(t *testing.T) {
	runNodeTests(t, "readerreconcile.test.mjs")
}

// Where a book reopens is decided without a browser: which chapter a
// foreign locator names, and what of it is worth following.
func TestReadingRestoreLadder(t *testing.T) {
	runNodeTests(t, "readerrestore.test.mjs")
}

func TestReadingAnchorCapture(t *testing.T) {
	runNodeTests(t, "readeranchor.test.mjs")
}
