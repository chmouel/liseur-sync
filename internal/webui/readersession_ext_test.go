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
