package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/chmouel/liseur-sync/internal/store"
)

const offlineRevisionPlaceholder = "__OFFLINE_SHELL_REVISION__"

// Render with a fixed nonce only for hashing; navigation responses get fresh ones.
var offlineWorker = sync.OnceValues(func() ([]byte, error) {
	var reader bytes.Buffer
	if err := readerPage(offlineReaderView("revision"), "").Render(context.Background(), &reader); err != nil {
		return nil, err
	}
	revision, err := offlineShellRevision(staticFS, reader.Bytes())
	if err != nil {
		return nil, err
	}
	script, err := fs.ReadFile(staticFS, "static/offline/sw.js")
	if err != nil {
		return nil, err
	}
	return bytes.ReplaceAll(script, []byte(offlineRevisionPlaceholder), []byte(revision)), nil
})

func offlineShellRevision(assets fs.FS, reader []byte) (string, error) {
	files := make(map[string]string, len(offlineAssetTypes)+len(offlineReaderAssets))
	for name, contentType := range offlineAssetTypes {
		files["static/offline/"+name] = contentType
	}
	for name, contentType := range offlineReaderAssets {
		files["static/"+name] = contentType
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%d\x00", name, files[name], len(data))
		hash.Write(data)
	}
	hash.Write(reader)
	hash.Write([]byte(readerPolicy("", "revision")))
	hash.Write([]byte(uiPolicy))
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

var offlineAssetTypes = map[string]string{
	"icon.svg":             "image/svg+xml",
	"icon-512.svg":         "image/svg+xml",
	"apple-touch-icon.png": "image/png",
	"manifest.json":        "application/manifest+json",
	"offline.css":          "text/css; charset=utf-8",
	"offline.js":           "application/javascript; charset=utf-8",
	"offline-shelf.js":     "application/javascript; charset=utf-8",
	"shell.html":           "text/html; charset=utf-8",
	"sw.js":                "application/javascript; charset=utf-8",
}

var offlineReaderAssets = map[string]string{
	"offline-account.js":        "application/javascript; charset=utf-8",
	"offline-storage.js":        "application/javascript; charset=utf-8",
	"offline-sync.js":           "application/javascript; charset=utf-8",
	"reader-annotations.js":     "application/javascript; charset=utf-8",
	"reader-app.js":             "application/javascript; charset=utf-8",
	"reader-auth.js":            "application/javascript; charset=utf-8",
	"reader-engine.js":          "application/javascript; charset=utf-8",
	"reader-live.js":            "application/javascript; charset=utf-8",
	"reader-positions.js":       "application/javascript; charset=utf-8",
	"reader-publication.js":     "application/javascript; charset=utf-8",
	"reader-session-upload.js":  "application/javascript; charset=utf-8",
	"reader-session.js":         "application/javascript; charset=utf-8",
	"reader-sync.js":            "application/javascript; charset=utf-8",
	"style.css":                 "text/css; charset=utf-8",
	"vendor/foliate/epubcfi.js": "application/javascript; charset=utf-8",
	"vendor/readium/readium.js": "application/javascript; charset=utf-8",
}

func offlineContent() fs.FS {
	sub, err := fs.Sub(staticFS, "static/offline")
	if err != nil {
		panic(err)
	}
	return sub
}

func handleOfflineAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/ui/offline/")
	if name == "sw.js" {
		script, err := offlineWorker()
		if err != nil {
			http.Error(w, "offline shell unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", offlineAssetTypes[name])
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(script)
		return
	}
	serveOfflineAsset(w, name, "no-cache")
}

func serveOfflineAsset(w http.ResponseWriter, name, cacheControl string) {
	contentType, ok := offlineAssetTypes[name]
	if !ok || strings.Contains(name, "/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	data, err := fs.ReadFile(offlineContent(), name)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func handleOfflineReaderAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	contentType, ok := offlineReaderAssets[name]
	if !ok || strings.Contains(name, "..") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	data, err := fs.ReadFile(staticFS, "static/"+name)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func (s *Server) handleOfflineShell(w http.ResponseWriter, r *http.Request, _ store.AuthSession, _ *store.User) {
	serveOfflineAsset(w, "shell.html", "no-store")
}

func (s *Server) handleOfflineAccount(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"account": u.ID,
		"csrf":    csrfFor(a),
	})
}
