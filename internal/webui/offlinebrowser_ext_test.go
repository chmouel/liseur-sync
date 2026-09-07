package webui_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOfflineIndexedDBInBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chromium not installed")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--test", filepath.Join("testdata", "offlinebrowser.test.mjs"))
	cmd.Env = append(os.Environ(), "SMOKE_CHROME="+chrome)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline IndexedDB browser checks: %v\n%s", err, output)
	}
}
