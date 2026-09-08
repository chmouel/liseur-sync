package webui

import (
	"bytes"
	"encoding/json"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/chmouel/liseur-sync/internal/config"
)

func TestOfflineShellRequiresSessionOnline(t *testing.T) {
	ts, _ := testServer(t)

	resp, err := noRedirect().Get(ts.URL + "/ui/offline/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "../login" {
		t.Fatalf("unauthenticated shell: got %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	cookie := loginCookie(t, ts)
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ui/offline/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	code, body := resp.StatusCode, string(bodyBytes)
	if code != http.StatusOK {
		t.Fatalf("authenticated shell: got %d", code)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != uiPolicy {
		t.Fatalf("offline shell CSP changed: %q", got)
	}
	for _, leaked := range []string{"alice", "hunter2hunter", `name="csrf"`, "v1/"} {
		if strings.Contains(body, leaked) {
			t.Errorf("offline shell leaked %q", leaked)
		}
	}
	if !strings.Contains(body, "Offline shelf") {
		t.Fatal("authenticated shell did not render the generic placeholder")
	}

	req, err = http.NewRequest(http.MethodGet, ts.URL+"/ui/offline/account", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var account struct {
		Account string `json:"account"`
		CSRF    string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&account); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || account.Account != "u1" || account.CSRF == "" {
		t.Fatalf("offline account bootstrap: status=%d account=%q csrf=%t",
			resp.StatusCode, account.Account, account.CSRF != "")
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("offline account cache policy: %q", got)
	}
}

func TestOfflineAssetsArePublicAndRelative(t *testing.T) {
	ts, _ := testServer(t)

	manifestResp, err := noRedirect().Get(ts.URL + "/ui/offline/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	defer manifestResp.Body.Close()
	if manifestResp.StatusCode != http.StatusOK {
		t.Fatalf("manifest: got %d", manifestResp.StatusCode)
	}
	var manifest struct {
		ID       string `json:"id"`
		StartURL string `json:"start_url"`
		Scope    string `json:"scope"`
		Display  string `json:"display"`
		Icons    []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.NewDecoder(manifestResp.Body).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "" || manifest.StartURL != "./" ||
		manifest.Scope != "./" || manifest.Display != "standalone" {
		t.Fatalf("manifest metadata is not stable and scoped: %+v", manifest)
	}
	if len(manifest.Icons) != 5 || manifest.Icons[0].Src != "./icon.svg" ||
		manifest.Icons[1].Src != "./icon-512.svg" {
		t.Fatalf("manifest icon is not relative: %+v", manifest.Icons)
	}
	for _, icon := range manifest.Icons {
		if !strings.HasPrefix(icon.Src, "./") {
			t.Fatalf("icon URL is not relative: %q", icon.Src)
		}
		resp, err := noRedirect().Get(ts.URL + "/ui/offline/" + strings.TrimPrefix(icon.Src, "./"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != icon.Type {
			t.Fatalf("icon %s: status %d, type %q", icon.Src, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if icon.Type == "image/png" {
			im, err := png.Decode(bytes.NewReader(body))
			if err != nil {
				t.Fatalf("icon %s: %v", icon.Src, err)
			}
			want := 512
			if icon.Sizes == "192x192" {
				want = 192
			}
			if im.Bounds().Dx() != want || im.Bounds().Dy() != want {
				t.Errorf("icon %s: dimensions %v do not match %s", icon.Src, im.Bounds(), icon.Sizes)
			}
			if icon.Purpose == "maskable" {
				for y := 0; y < want; y++ {
					for x := 0; x < want; x++ {
						if _, _, _, a := im.At(x, y).RGBA(); a != 0xffff {
							t.Fatalf("maskable icon is transparent at %d,%d", x, y)
						}
					}
				}
			}
		}
	}

	for _, name := range []string{"shell.html", "offline.css", "offline.js", "offline-shelf.js", "icon.svg", "icon-512.svg", "apple-touch-icon.png", "sw.js"} {
		resp, err := noRedirect().Get(ts.URL + "/ui/offline/" + name)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: got %d", name, resp.StatusCode)
		}
		if strings.Contains(string(body), "alice") || strings.Contains(string(body), "csrf") {
			t.Errorf("%s contains personalized data", name)
		}
	}
	for _, name := range []string{"reader-app.js", "reader-engine.js", "reader-publication.js", "vendor/readium/readium.js"} {
		resp, err := noRedirect().Get(ts.URL + "/ui/offline/assets/" + name)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("reader asset %s: got %d", name, resp.StatusCode)
		}
		if strings.Contains(string(body), "hunter2hunter") {
			t.Errorf("reader asset %s contains credential material", name)
		}
	}
}

func TestOfflineServiceWorkerIsBoundedToShellAssets(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := noRedirect().Get(ts.URL + "/ui/offline/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	script := string(body)
	for _, required := range []string{
		"liseur-sync-offline-shell-",
		"./shell.html",
		"./manifest.json",
		"./offline.css",
		"./offline.js",
		"./offline-shelf.js",
		"./icon.svg",
		"./icon-512.svg",
		"./icon-192.png",
		"./icon-512.png",
		"./icon-maskable-512.png",
		"request.mode === 'navigate'",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("service worker missing %q", required)
		}
	}
	for _, forbidden := range []string{"/v1/", "Authorization", "token", "book"} {
		if strings.Contains(strings.ToLower(script), strings.ToLower(forbidden)) {
			t.Errorf("service worker contains forbidden %q", forbidden)
		}
	}
	for _, asset := range []string{
		"./read/",
		"./assets/offline-account.js",
		"./assets/offline-storage.js",
		"./assets/reader-app.js",
		"./assets/vendor/readium/readium.js",
	} {
		if !strings.Contains(script, asset) {
			t.Errorf("service worker missing reader asset %q", asset)
		}
	}
	if strings.Contains(script, "cache.put") {
		t.Fatal("service worker must not cache network responses")
	}
	if strings.Contains(script, offlineRevisionPlaceholder) {
		t.Fatal("service worker revision was not rendered")
	}
	if strings.Contains(script, "skipWaiting") {
		t.Fatal("an upgrade must wait for readers using the old shell to close")
	}
}

func TestOfflineShellRevisionTracksEmbeddedAssetsAndReader(t *testing.T) {
	assets := fstest.MapFS{}
	err := fs.WalkDir(staticFS, "static", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(staticFS, path)
		if err != nil {
			return err
		}
		assets[path] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := func(reader string) string {
		t.Helper()
		got, err := offlineShellRevision(assets, []byte(reader))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	baseline := revision("reader")
	if again := revision("reader"); again != baseline {
		t.Fatal("unchanged assets produced a different worker revision")
	}
	for path := range assets {
		_, isReaderAsset := offlineReaderAssets[strings.TrimPrefix(path, "static/")]
		_, isShellAsset := offlineAssetTypes[strings.TrimPrefix(path, "static/offline/")]
		if !isReaderAsset && !isShellAsset {
			continue
		}
		old := assets[path]
		assets[path] = &fstest.MapFile{Data: append(append([]byte(nil), old.Data...), '\n')}
		if revision("reader") == baseline {
			t.Errorf("changing %s did not update the worker", path)
		}
		assets[path] = old
	}
	if revision("new reader") == baseline {
		t.Fatal("changing the reader shell did not update the worker")
	}
	delete(assets, "static/offline/shell.html")
	if _, err := offlineShellRevision(assets, nil); err == nil {
		t.Fatal("missing shell asset must fail revision generation")
	}
}

func TestUILinksToRelativeOfflineInstallSurface(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/library")
	if code != http.StatusOK {
		t.Fatalf("library: got %d", code)
	}
	for _, want := range []string{
		`rel="manifest" href="offline/manifest.json"`,
		`data-pwa-base="offline/"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("library missing relative PWA surface %q", want)
		}
	}
}

func TestSeparateReaderOriginDisablesOfflineSurface(t *testing.T) {
	ts, _ := testServerCfg(t, func(cfg *config.Config) {
		cfg.ReaderOrigin = "https://reader.example.com"
	}, nil)

	for _, path := range []string{"/ui/offline/", "/ui/offline/manifest.json"} {
		resp, err := noRedirect().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, resp.StatusCode)
		}
	}

	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/library")
	if code != http.StatusOK {
		t.Fatalf("library: got %d", code)
	}
	for _, absent := range []string{
		`rel="manifest"`,
		`data-pwa-base="offline/`,
		`offline-account.js`,
		`offline-download.js`,
		`href="offline/"`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("separate reader origin still exposes %q", absent)
		}
	}
}

func TestOfflineInstallURLsStayUnderTheUIPath(t *testing.T) {
	for path, want := range map[string]string{
		"/ui/":                "offline/",
		"/ui/library":         "offline/",
		"/ui/books/book-id":   "../offline/",
		"/ui/books/book/read": "../../offline/",
	} {
		if got := relPrefix(path) + "offline/"; got != want {
			t.Errorf("%s: offline base %q, want %q", path, got, want)
		}
	}
}
