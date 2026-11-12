package webui

import (
	"archive/zip"
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

//go:embed koreader/1-liseur-setup.lua.tmpl
var koreaderSetupSource string

var koreaderSetupTemplate = template.Must(template.New("setup").Funcs(template.FuncMap{"lua": luaString}).Parse(koreaderSetupSource))

// koreaderSetup holds one reader's credentials. Each field is a separate
// credential: the OPDS token, the Progress sync login and the KoInsight
// capability URL can each be revoked on their own.
type koreaderSetup struct {
	ID, Name, Base         string
	APIToken               string
	SyncSlot, SyncPassword string
	SyncKey                string
	SyncURL, StatisticsURL string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// kosyncSlot is the Progress sync username. It is a lowercase slug of the
// reader's name with a random suffix, so two downloads for one name do not
// collide on the device slot.
func kosyncSlot(name string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "reader"
	}
	return slug + "-" + uuid.NewString()[:8]
}

func luaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range []byte(s) {
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 32 || c == 127:
			fmt.Fprintf(&b, "\\%03d", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (s *Server) handleKOReaderSetup(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if name == "" || !utf8.ValidString(name) || len(name) > 100 || auth.ReservedTokenName(name) || strings.ContainsAny(name, "\x00\r\n") {
		http.Error(w, "device name must be 1–100 bytes and cannot contain line breaks or use a reserved name", http.StatusBadRequest)
		return
	}
	if !s.Cfg.Adapters.Kosync || !u.KosyncEnabled || !s.Cfg.Adapters.Koplugin || !u.KopluginEnabled {
		http.Error(w, "enable Progress sync and the statistics adapter before downloading KOReader setup", http.StatusConflict)
		return
	}
	ctx := r.Context()
	syncPassword, err := auth.NewSecret()
	if err != nil {
		http.Error(w, "could not prepare setup", http.StatusInternalServerError)
		return
	}
	syncPassword = syncPassword[:32]
	// KOReader sends md5(password) as the Progress sync key, so the server
	// stores the hash of that digest and never the password itself.
	syncKey := auth.KosyncUserKey(syncPassword)
	capability, err := auth.NewSecret()
	if err != nil {
		http.Error(w, "could not prepare setup", http.StatusInternalServerError)
		return
	}
	apiToken, tok, err := s.Auth.MintToken(ctx, u.ID, name, store.ScopeSet{store.ScopeLibraryRead}, nil)
	if err != nil {
		http.Error(w, "could not create API token", http.StatusInternalServerError)
		return
	}
	slot := kosyncSlot(name)
	statsID := uuid.NewString()
	complete := false
	defer func() {
		if !complete {
			rctx := context.WithoutCancel(ctx)
			_ = s.St.RevokeToken(rctx, u.ID, tok.ID)
			_ = s.St.RevokeKosyncDevice(rctx, u.ID, slot)
			_ = s.St.RevokeKopluginDevice(rctx, u.ID, statsID)
		}
	}()
	if err := s.St.CreateKosyncDevice(ctx, store.KosyncDevice{
		UserID: u.ID, DeviceSlot: slot, KeySHA256: auth.HashSecret(syncKey), Label: name,
	}); err != nil {
		http.Error(w, "could not create Progress sync device", http.StatusInternalServerError)
		return
	}
	if err := s.St.CreateKopluginDevice(ctx, store.KopluginDevice{
		ID: statsID, UserID: u.ID, TokenSHA256: auth.HashSecret(capability),
		Label: name, DeviceID: tok.DeviceID, CreatedAt: time.Now(),
	}); err != nil {
		http.Error(w, "could not create statistics credential", http.StatusInternalServerError)
		return
	}
	base := s.serverBaseURL(r)
	setup := koreaderSetup{
		ID: statsID, Name: name, Base: base,
		APIToken: apiToken, SyncSlot: slot, SyncPassword: syncPassword, SyncKey: syncKey,
		SyncURL: base + "/adapter/kosync", StatisticsURL: kopluginURL(base, capability),
	}
	bundle, err := setup.archive()
	if err != nil {
		http.Error(w, "could not prepare setup", http.StatusInternalServerError)
		return
	}
	complete = true
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="liseur-koreader-setup.zip"`)
	w.Write(bundle)
}

func (s koreaderSetup) archive() ([]byte, error) {
	var patch bytes.Buffer
	if err := koreaderSetupTemplate.Execute(&patch, s); err != nil {
		return nil, err
	}
	// F-Droid's KOReader never runs patches but does load plugins. The same
	// script therefore ships as a plugin: main.lua runs it at load, then
	// returns disabled so the loader skips it.
	pluginMain := "local function liseur_setup()\n" + patch.String() +
		"\nend\nlocal ok, err = pcall(liseur_setup)\nif not ok then\n" +
		"    require(\"logger\").warn(\"Liseur setup failed:\", err)\nend\nreturn { disabled = true }\n"
	pluginMeta := "return {\n    name = \"liseur_setup\",\n    fullname = \"Liseur setup\",\n" +
		"    description = [[One-time Liseur Sync setup. Safe to delete after the first start.]],\n}\n"
	credentials := fmt.Sprintf(`Server: %s

OPDS catalog: %s/opds/v1.2
  username: token
  password: %s
OPDS 2.0 catalog for the OPDS Plus plugin, same login: %s/opds/v2

Progress sync (KOReader's built-in module)
  custom sync server: %s
  username: %s
  password: %s

KoInsight server URL: %s
`, s.Base, s.Base, s.APIToken, s.Base, s.SyncURL, s.SyncSlot, s.SyncPassword, s.StatisticsURL)
	readme := fmt.Sprintf(`KOReader setup for %s

Automatic setup (KOReader with user patches enabled):
1. Quit KOReader completely.
2. Copy patches/1-liseur-setup.lua into KOReader's patches/ folder
   (create the folder if it does not exist).
3. Turn Wi-Fi on and start KOReader. Progress sync, the OPDS catalog and
   KoInsight are set up, and the KoInsight plugin is downloaded if it is
   missing. If the reader was offline, it tries again on the next start.
4. Delete patches/1-liseur-setup.lua once the KoInsight plugin is in
   KOReader's plugins/ folder.

If the OPDS Plus plugin (github.com/greywolf1499/opds_plus.koplugin) is
installed, the setup also adds the OPDS 2.0 catalog to it. Install the
plugin before deleting the setup file and it is added on the next start.

KOReader from F-Droid on Android disables user patches. There, copy
plugins/liseur-setup.koplugin (the whole folder) into KOReader's plugins/
folder instead, with Wi-Fi on, then start KOReader and restart it once more
so the new settings and the KoInsight plugin are picked up. Delete the
folder afterwards.

Manual setup: enter the values in credentials.txt.
- Progress sync: Tools > Progress sync > Custom sync server, then
  Register / Login with the username and password.
- OPDS: Search > OPDS catalog, add the catalog URL with the username and password.
  With OPDS Plus, add the OPDS 2.0 catalog URL there with the same login.
- KoInsight: install koinsight.koplugin from github.com/Ko-Insight/KoInsight
  (plugins/ folder), then Tools > KoInsight > Set server URL.

The setup runs once and keeps unrelated settings. It replaces any Progress
sync account already configured on this reader.

This archive contains private credentials. Keep it private and use it on one
reader only. Download a separate setup for each reader. Delete this archive
and the copies on the reader after setup. To cut this reader off, revoke all
three on the server's Devices page: the API token, the Progress sync device
%s and the statistics device named %s.
`, s.Name, s.SyncSlot, s.Name)
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, file := range []struct{ name, body string }{
		{"patches/1-liseur-setup.lua", patch.String()},
		{"plugins/liseur-setup.koplugin/_meta.lua", pluginMeta},
		{"plugins/liseur-setup.koplugin/main.lua", pluginMain},
		{"credentials.txt", credentials},
		{"README.txt", readme},
	} {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetMode(0600)
		w, err := z.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(file.body)); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
