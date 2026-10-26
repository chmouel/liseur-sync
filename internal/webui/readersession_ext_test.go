package webui_test

import (
	"testing"
)

// TestReaderSessionAccounting runs the node unit tests for the reader's
// session arithmetic (static/reader-session.js). The module has no DOM
// and no fetch in it precisely so that its rules — the idle cap, the
// minimum, the clamps — can be checked without a browser.
func TestReaderSessionAccounting(t *testing.T) {
	runNodeTests(t, "readersession.test.mjs")
}

func TestReaderSessionUploads(t *testing.T) {
	runNodeTests(t, "readersessionupload.test.mjs")
}

func TestReaderAudioLifecycle(t *testing.T) {
	runNodeTests(t, "readeraudio.test.mjs")
}

// TestReaderNarration runs the node unit tests for the audiobook
// sentence highlight (static/reader-narration.js): how a chapter's text
// is counted, split into sentences and kept in step with the narrator.
func TestReaderNarration(t *testing.T) {
	runNodeTests(t, "readernarration.test.mjs")
}
