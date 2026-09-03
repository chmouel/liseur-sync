//go:build linux

package content

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/chmouel/liseur-sync/internal/store"
)

// These are constants rather than settings. A person adding a folder to
// a book server has no way to choose a good debounce, and offering the
// choice would only be one more thing to get wrong.
const (
	// watchDebounce is how long a burst of filesystem events is allowed
	// to settle before a pass runs. Copying a book in is many events,
	// and reconciling after each one would read the same half-written
	// file repeatedly.
	watchDebounce = 3 * time.Second
	// safetyInterval is the periodic pass that runs regardless of
	// events. inotify does not see changes on NFS or SMB, and it drops
	// events under pressure, so a folder that is only ever reconciled on
	// notification will eventually be wrong. The interval is long
	// because this is a safety net, not the mechanism.
	safetyInterval = 30 * time.Minute
)

// FolderSource is the store surface the watcher needs to know what to
// watch. It is separate from Catalog because reading the folder list and
// reconciling one folder are different concerns and a test usually wants
// only one of them.
type FolderSource interface {
	ListFolders(ctx context.Context, viewerID, after string, limit int) ([]store.Folder, error)
	FolderByID(ctx context.Context, viewerID, folderID string) (store.Folder, error)
}

// Watcher keeps the catalog reflecting the disk.
//
// It runs a pass at startup, whenever a folder's tree changes, and on a
// slow timer. That is the whole mechanism: there is no queue to inspect,
// no job to retry and no state that survives a restart, because a pass
// is idempotent and the disk is the only source of truth.
type Watcher struct {
	folders     FolderSource
	reconciler  *Reconciler
	log         *slog.Logger
	notify      *fsnotify.Watcher
	events      chan string
	mu          sync.Mutex
	reconcileMu sync.Mutex
	watched     map[string]store.Folder
	watchedDirs map[string]string
}

// NewWatcher builds a watcher. A server that cannot create an inotify
// instance at all still starts: every folder falls back to the periodic
// pass, with a warning, because serving a slightly stale catalog is
// better than not serving one.
func NewWatcher(
	folders FolderSource, reconciler *Reconciler, log *slog.Logger,
) *Watcher {
	if log == nil {
		log = slog.Default()
	}
	notify, err := fsnotify.NewWatcher()
	if err != nil {
		log.Warn("filesystem notifications unavailable; "+
			"folders will be reconciled on the periodic pass only",
			"error", err)
		notify = nil
	}
	return &Watcher{
		folders:     folders,
		reconciler:  reconciler,
		log:         log,
		notify:      notify,
		events:      make(chan string, 64),
		watched:     map[string]store.Folder{},
		watchedDirs: map[string]string{},
	}
}

// Run reconciles every folder, then keeps reconciling as things change.
// It returns when ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	defer func() {
		if w.notify != nil {
			_ = w.notify.Close()
		}
	}()

	folders, err := w.allFolders(ctx)
	if err != nil {
		return err
	}
	for _, folder := range folders {
		w.Add(ctx, folder)
	}

	if w.notify != nil {
		go w.forwardEvents(ctx)
	}

	pending := map[string]time.Time{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	safety := time.NewTicker(safetyInterval)
	defer safety.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case folderID := <-w.events:
			pending[folderID] = time.Now().Add(watchDebounce)
		case <-tick.C:
			now := time.Now()
			for folderID, due := range pending {
				if now.Before(due) {
					continue
				}
				delete(pending, folderID)
				w.reconcileOne(ctx, folderID)
			}
		case <-safety.C:
			// The periodic pass reads the folder list again rather than
			// trusting the watcher's own map: it is the thing that
			// notices a folder somebody added while an event was being
			// missed.
			folders, err := w.allFolders(ctx)
			if err != nil {
				w.log.Warn("periodic pass could not list folders", "error", err)
				continue
			}
			for _, folder := range folders {
				w.Add(ctx, folder)
				_, _ = w.reconcile(ctx, folder)
			}
		}
	}
}

// Add registers a folder and reconciles it immediately. This is what
// makes "add a folder and the books show up" true without a restart.
func (w *Watcher) Add(ctx context.Context, folder store.Folder) {
	w.mu.Lock()
	_, already := w.watched[folder.ID]
	w.watched[folder.ID] = folder
	w.mu.Unlock()

	w.watchTree(folder)
	if !already {
		_, _ = w.reconcile(ctx, folder)
	}
}

// Remove unregisters a folder. Its catalog rows go with the folder row;
// nothing under its root is touched.
func (w *Watcher) Remove(folderID string) {
	w.mu.Lock()
	folder, ok := w.watched[folderID]
	delete(w.watched, folderID)
	var paths []string
	for dir, owner := range w.watchedDirs {
		if owner == folderID {
			paths = append(paths, dir)
			delete(w.watchedDirs, dir)
		}
	}
	w.mu.Unlock()
	if !ok || w.notify == nil {
		return
	}
	for _, dir := range paths {
		_ = w.notify.Remove(dir)
	}
	w.log.Info("stopped watching folder", "folder", folder.ID, "name", folder.Name)
}

// watchTree puts an inotify watch on a folder's root and every directory
// beneath it, because inotify is not recursive.
//
// A root that cannot be watched — an inotify limit, an NFS mount — is a
// warning and not a failure. The folder still gets its periodic pass,
// which is a slower answer rather than no answer.
func (w *Watcher) watchTree(folder store.Folder) {
	if w.notify == nil {
		return
	}
	added := 0
	err := filepath.WalkDir(folder.RootPath, func(pathname string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		w.mu.Lock()
		_, have := w.watchedDirs[pathname]
		w.mu.Unlock()
		if have {
			return nil
		}
		if err := w.notify.Add(pathname); err != nil {
			return nil
		}
		w.mu.Lock()
		w.watchedDirs[pathname] = folder.ID
		w.mu.Unlock()
		added++
		return nil
	})
	if err != nil || added == 0 {
		w.log.Warn("watching folder by periodic pass only",
			"folder", folder.ID, "root", folder.RootPath, "error", err)
	}
}

// forwardEvents translates filesystem events into folder ids. A
// directory created under a watched root gets its own watch, because
// otherwise a whole series copied in at once would be invisible until
// the periodic pass.
func (w *Watcher) forwardEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.notify.Events:
			if !ok {
				return
			}
			folderID := w.ownerOf(event.Name)
			if folderID == "" {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Lstat(event.Name); err == nil && info.IsDir() {
					w.mu.Lock()
					folder, known := w.watched[folderID]
					w.mu.Unlock()
					if known {
						w.watchTree(folder)
					}
				}
			}
			select {
			case w.events <- folderID:
			default:
				// The channel is a signal, not a log. Dropping one when
				// a pass is already pending costs nothing, because the
				// pass reads the whole folder anyway.
			}
		case err, ok := <-w.notify.Errors:
			if !ok {
				return
			}
			w.log.Warn("filesystem watch error", "error", err)
		}
	}
}

// ownerOf finds which folder a changed path belongs to by looking up its
// directory, which is what was watched.
func (w *Watcher) ownerOf(pathname string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if id, ok := w.watchedDirs[pathname]; ok {
		return id
	}
	return w.watchedDirs[filepath.Dir(pathname)]
}

func (w *Watcher) reconcileOne(ctx context.Context, folderID string) {
	w.mu.Lock()
	folder, ok := w.watched[folderID]
	w.mu.Unlock()
	if !ok {
		return
	}
	_, _ = w.reconcile(ctx, folder)
}

// ScanFolders runs a pass over each of these folders in turn and waits
// for all of them, returning what they changed between them.
//
// One folder that fails does not stop the rest: an unreachable mount is
// exactly the situation somebody presses a scan button in, and leaving
// every other folder unscanned because of it would be the wrong answer.
// The failures come back joined, so the caller can still say so.
func (w *Watcher) ScanFolders(
	ctx context.Context, folders []store.Folder,
) (store.ReconcileResult, error) {
	var total store.ReconcileResult
	var failures []error
	for _, folder := range folders {
		if err := ctx.Err(); err != nil {
			return total, errors.Join(append(failures, err)...)
		}
		res, err := w.reconcile(ctx, folder)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", folder.Name, err))
			continue
		}
		total.Added += res.Added
		total.Updated += res.Updated
		total.Replaced += res.Replaced
		total.Missing += res.Missing
		total.Returned += res.Returned
		total.Purged += res.Purged
		total.Rekeyed += res.Rekeyed
	}
	return total, errors.Join(failures...)
}

// reconcile runs one pass and logs what changed. A pass that changed
// nothing is logged at debug, because the common case is a folder nobody
// touched and a line per folder per half hour is noise.
func (w *Watcher) reconcile(ctx context.Context, folder store.Folder) (store.ReconcileResult, error) {
	w.reconcileMu.Lock()
	defer w.reconcileMu.Unlock()
	result, err := w.reconciler.Reconcile(ctx, folder)
	if err != nil {
		w.log.Warn("folder pass failed",
			"folder", folder.ID, "name", folder.Name, "error", err)
		return store.ReconcileResult{}, err
	}
	if !result.Changed() {
		w.log.Debug("folder unchanged", "folder", folder.ID, "name", folder.Name)
		return result, nil
	}
	w.log.Info("folder reconciled",
		"folder", folder.ID, "name", folder.Name,
		"added", result.Added, "updated", result.Updated,
		"replaced", result.Replaced, "missing", result.Missing,
		"returned", result.Returned,
		"purged", result.Purged, "rekeyed", result.Rekeyed)
	return result, nil
}

func (w *Watcher) allFolders(ctx context.Context) ([]store.Folder, error) {
	var all []store.Folder
	cursor := ""
	for {
		page, err := w.folders.ListFolders(ctx, "", cursor, 200)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 200 {
			return all, nil
		}
		cursor = store.FolderCursor(page[len(page)-1])
	}
}
