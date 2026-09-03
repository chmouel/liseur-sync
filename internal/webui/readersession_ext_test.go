package webui_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestReaderSessionAccounting runs the node unit tests for the reader's
// session arithmetic (static/reader-session.js). The module has no DOM
// and no fetch in it precisely so that its rules — the idle cap, the
// minimum, the clamps — can be checked without a browser; this is the
// one reader check that runs in CI.
func TestReaderSessionAccounting(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to run the session tests with")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--test", filepath.Join("testdata", "readersession.test.mjs"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("session accounting tests failed: %v\n%s", err, out)
	}
}
