//go:build linux

package content

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// captureWatcher builds a watcher that logs where the test can read it.
// Only watchTree is exercised, so it needs no folders and no reconciler.
func captureWatcher(t *testing.T) (*Watcher, *bytes.Buffer) {
	t.Helper()
	var logged bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	w := NewWatcher(nil, nil, log)
	if w.notify == nil {
		t.Skip("no inotify instance available")
	}
	t.Cleanup(func() { _ = w.notify.Close() })
	return w, &logged
}

func periodicPassWarnings(t *testing.T, logged *bytes.Buffer) []string {
	t.Helper()
	var causes []string
	for _, line := range strings.Split(strings.TrimSpace(logged.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if entry["msg"] != "watching folder by periodic pass only" {
			continue
		}
		cause, _ := entry["error"].(string)
		causes = append(causes, cause)
	}
	return causes
}

// TestARewatchedFolderIsNotReportedAsUnwatched is the second half of
// issue #33: a folder that was being watched perfectly well was reported
// as unwatched twice an hour, because every periodic pass registers it
// again and the second registration has nothing left to add.
func TestARewatchedFolderIsNotReportedAsUnwatched(t *testing.T) {
	w, logged := captureWatcher(t)
	folder := plainFolder(t)
	writeBook(t, folder.RootPath, "series/one.epub", "One")

	w.watchTree(folder)
	if got := periodicPassWarnings(t, logged); len(got) != 0 {
		t.Fatalf("first watch warned %d times: %q", len(got), got)
	}
	logged.Reset()

	for range 3 {
		w.watchTree(folder)
	}
	if got := periodicPassWarnings(t, logged); len(got) != 0 {
		t.Fatalf("re-watching an already watched tree warned %d times: %q", len(got), got)
	}
}

// TestAnUnwatchableTreeWarnsWithACause leaves the warning its job. A
// tree nothing is watching must still say so, and say why: the reporter
// of issue #33 spent their evening on an error=<nil> that meant nothing.
func TestAnUnwatchableTreeWarnsWithACause(t *testing.T) {
	t.Run("inotify refuses the watch", func(t *testing.T) {
		w, logged := captureWatcher(t)
		// A closed instance refuses every Add, which is what running out
		// of inotify watches looks like from here.
		if err := w.notify.Close(); err != nil {
			t.Fatal(err)
		}

		w.watchTree(plainFolder(t))

		got := periodicPassWarnings(t, logged)
		if len(got) != 1 {
			t.Fatalf("warnings: %v", got)
		}
		if got[0] == "" {
			t.Fatal("a folder on the periodic pass only reported no cause")
		}
	})

	t.Run("the root cannot be walked", func(t *testing.T) {
		w, logged := captureWatcher(t)
		folder := plainFolder(t)
		folder.RootPath = filepath.Join(folder.RootPath, "gone")

		w.watchTree(folder)

		got := periodicPassWarnings(t, logged)
		if len(got) != 1 {
			t.Fatalf("warnings: %v", got)
		}
		if !strings.Contains(got[0], "no such file") {
			t.Fatalf("cause did not name the missing root: %q", got[0])
		}
	})

	t.Run("an inner folder is watched as part of an outer one", func(t *testing.T) {
		w, logged := captureWatcher(t)
		outer := plainFolder(t)
		inner := store.Folder{
			ID: "f2", Name: "Literature", Kind: store.FolderPlain,
			RootPath: filepath.Join(outer.RootPath, "literature"),
		}
		if err := os.Mkdir(inner.RootPath, 0o755); err != nil {
			t.Fatal(err)
		}

		w.watchTree(outer)
		logged.Reset()
		w.watchTree(inner)

		// The outer folder owns those directories, so the inner folder's
		// events are delivered to the outer one and the inner folder
		// really is on the periodic pass.
		got := periodicPassWarnings(t, logged)
		if len(got) != 1 {
			t.Fatalf("warnings: %q", got)
		}
		if !strings.Contains(got[0], outer.ID) {
			t.Fatalf("cause did not name the folder holding the watch: %q", got[0])
		}
	})

	t.Run("a subdirectory is unreadable but the root is watched", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads a 0000 directory")
		}
		w, logged := captureWatcher(t)
		folder := plainFolder(t)
		closed := filepath.Join(folder.RootPath, "closed")
		if err := os.Mkdir(closed, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(closed, 0o700) })

		w.watchTree(folder)

		// The root is watched, so events still arrive.
		if got := periodicPassWarnings(t, logged); len(got) != 0 {
			t.Fatalf("a watched root warned because of one bad child: %q", got)
		}
	})
}
