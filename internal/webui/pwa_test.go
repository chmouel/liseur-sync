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
	for _, name := range []string{"reader-app.js", "reader-chapters.js", "reader-engine.js", "reader-publication.js", "vendor/readium/readium.js"} {
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
		"./assets/reader-chapters.js",
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

// TestRailReachesTheOfflineShelfProgressively pins the entry that makes
// the shelf findable before the network is gone. It ships hidden and a
// script reveals it, because a browser without a service worker can
// never keep the shelf reachable and must not be offered it.
func TestRailReachesTheOfflineShelfProgressively(t *testing.T) {
	ts, _ := testServer(t)
	cookie := loginCookie(t, ts)
	code, body := page(t, ts, cookie, "/ui/library")
	if code != http.StatusOK {
		t.Fatalf("library: got %d", code)
	}
	if !strings.Contains(body, `href="offline/" data-rail-offline hidden`) {
		t.Fatalf("the rail does not carry a hidden Offline entry: %s", body)
	}
	if !strings.Contains(body, `src="static/offline-rail.js"`) {
		t.Fatal("the page does not load the script that reveals the entry")
	}
	// Between the two places books live, not after the account pages.
	library := strings.Index(body, `href="library"`)
	offline := strings.Index(body, `data-rail-offline`)
	insights := strings.Index(body, `href="insights"`)
	if library < 0 || offline < library || insights < offline {
		t.Fatalf("the Offline entry is not between Library and Insights: %d %d %d",
			library, offline, insights)
	}
	// The shelf itself is a page of the online UI too, not only a
	// fallback the service worker produces when the network is gone.
	code, shell := page(t, ts, cookie, "/ui/offline/")
	if code != http.StatusOK || !strings.Contains(shell, "Offline shelf") {
		t.Fatalf("the shelf is not reachable online: %d", code)
	}
}

// TestOfflineShellDecidesItsLibraryLinkBeforePaint: the installed app
// has no back button, so the shelf must never paint a link the device
// cannot follow. offline-net.js answers on <html> while the head is
// parsed, and the stylesheet trusts that answer alone.
func TestOfflineShellDecidesItsLibraryLinkBeforePaint(t *testing.T) {
	assets := map[string][]byte{}
	for _, name := range []string{"shell.html", "offline.css"} {
		data, err := fs.ReadFile(staticFS, "static/offline/"+name)
		if err != nil {
			t.Fatal(err)
		}
		assets[name] = data
	}
	shell := string(assets["shell.html"])
	for _, want := range []string{
		`<script src="./offline-net.js"></script>`,
		`id="offline-open-library"`,
		`class="btn offline-needs-network"`,
		`id="offline-network-note"`,
		`You're offline, your library will be back when you reconnect.`,
		`id="offline-brand"`,
	} {
		if !strings.Contains(shell, want) {
			t.Errorf("the offline shell is missing %q", want)
		}
	}
	if strings.Index(shell, "offline-net.js") > strings.Index(shell, `<link rel="stylesheet"`) {
		t.Fatal("the network script must run before the first paint, not after the stylesheets")
	}
	css := string(assets["offline.css"])
	if !strings.Contains(css, `html[data-connection="online"] .offline-needs-network`) {
		t.Fatal("the stylesheet does not hold the library link back until the network says so")
	}
}

// TestOfflineShellKnowsItsThemeAndSafeAreas is #112 on the cached shell:
// it needs viewport-fit=cover for the safe-area padding in offline.css
// to mean anything, and the status bar's colour has to follow the
// palette offline.js applies from the cookie.
func TestOfflineShellKnowsItsThemeAndSafeAreas(t *testing.T) {
	shell, err := fs.ReadFile(staticFS, "static/offline/shell.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`content="width=device-width, initial-scale=1, viewport-fit=cover"`,
		`name="apple-mobile-web-app-capable"`,
		`id="offline-theme-color"`,
		`media="(prefers-color-scheme: light)" content="#ffffff"`,
		`id="offline-theme-color-dark"`,
		`media="(prefers-color-scheme: dark)" content="#18181a"`,
	} {
		if !strings.Contains(string(shell), want) {
			t.Errorf("the offline shell is missing %q", want)
		}
	}

	script, err := fs.ReadFile(staticFS, "static/offline/offline.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`THEME_COLORS = {`,
		`"#18181a"`,
		`"#16161e"`,
		`"#1f1d2e"`,
		`document.getElementById("offline-theme-color")`,
		`document.getElementById("offline-theme-color-dark")?.remove()`,
	} {
		if !strings.Contains(string(script), want) {
			t.Errorf("the offline script does not keep the theme-color in step: missing %q", want)
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
