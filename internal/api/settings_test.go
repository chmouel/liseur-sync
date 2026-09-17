//go:build linux

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

func TestSettingsSyncRoundTrip(t *testing.T) {
	f := newFolderFixture(t)

	// GET on fresh account returns empty settings.
	code, body := getJSON(t, f.ts.URL+"/v1/me/settings", f.token)
	if code != http.StatusOK {
		t.Fatalf("get: %d %v", code, body)
	}
	settings, ok := body["settings"].(map[string]any)
	if !ok || len(settings) != 0 {
		t.Fatalf("fresh account has settings: %v", body)
	}

	// PUT two keys.
	putBody := `{"settings":{
		"reader.font":{"value":"literata","updated_at":"2026-06-01T12:00:00Z"},
		"reader.theme":{"value":"sepia","updated_at":"2026-06-01T12:00:00Z"}
	}}`
	code, body = putJSONReq(t, f.ts.URL+"/v1/me/settings", f.token, putBody)
	if code != http.StatusOK {
		t.Fatalf("put: %d %v", code, body)
	}
	settings = body["settings"].(map[string]any)
	if len(settings) != 2 {
		t.Fatalf("want 2 settings, got %v", settings)
	}

	// GET returns both.
	code, body = getJSON(t, f.ts.URL+"/v1/me/settings", f.token)
	if code != http.StatusOK {
		t.Fatalf("get after put: %d %v", code, body)
	}
	settings = body["settings"].(map[string]any)
	font := settings["reader.font"].(map[string]any)
	if font["value"] != "literata" {
		t.Fatalf("reader.font = %v", font)
	}

	// Last-writer-wins: newer timestamp overwrites.
	putBody = `{"settings":{
		"reader.font":{"value":"vollkorn","updated_at":"2026-06-02T12:00:00Z"}
	}}`
	code, body = putJSONReq(t, f.ts.URL+"/v1/me/settings", f.token, putBody)
	if code != http.StatusOK {
		t.Fatalf("put newer: %d %v", code, body)
	}
	settings = body["settings"].(map[string]any)
	font = settings["reader.font"].(map[string]any)
	if font["value"] != "vollkorn" {
		t.Fatalf("newer write lost: %v", font)
	}
	theme := settings["reader.theme"].(map[string]any)
	if theme["value"] != "sepia" {
		t.Fatalf("untouched key changed: %v", theme)
	}

	// Stale write is silently dropped.
	putBody = `{"settings":{
		"reader.font":{"value":"inter","updated_at":"2026-06-01T12:00:00Z"}
	}}`
	code, _ = putJSONReq(t, f.ts.URL+"/v1/me/settings", f.token, putBody)
	if code != http.StatusOK {
		t.Fatalf("stale put: %d", code)
	}
	code, body = getJSON(t, f.ts.URL+"/v1/me/settings", f.token)
	if code != http.StatusOK {
		t.Fatalf("get after stale: %d", code)
	}
	font = body["settings"].(map[string]any)["reader.font"].(map[string]any)
	if font["value"] != "vollkorn" {
		t.Fatalf("stale write overwrote: %v", font)
	}

	// Cross-user isolation.
	otherSync := f.mintToken(t, f.other.ID, store.ScopeSync)
	code, body = getJSON(t, f.ts.URL+"/v1/me/settings", otherSync)
	if code != http.StatusOK {
		t.Fatalf("other user get: %d", code)
	}
	otherSettings := body["settings"].(map[string]any)
	if len(otherSettings) != 0 {
		t.Fatalf("user isolation broken: %v", otherSettings)
	}

	// Scope gates.
	readOnly := f.mintToken(t, f.user.ID, store.ScopeLibraryRead)
	if code, _ = getJSON(t, f.ts.URL+"/v1/me/settings", readOnly); code != http.StatusForbidden {
		t.Fatalf("library-read reached settings: %d", code)
	}
	if code, _ = getJSON(t, f.ts.URL+"/v1/me/settings", ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", code)
	}
}

func putJSONReq(t *testing.T, url, token, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestSettingsRejectsUnusableInput covers the validation that stands
// between a client and a key nobody can write again, or a 500 that
// depends on which backend the server was built with.
func TestSettingsRejectsUnusableInput(t *testing.T) {
	f := newFolderFixture(t)
	url := f.ts.URL + "/v1/me/settings"

	// A timestamp far in the future would win every later comparison,
	// and with no delete route the key could never be written again.
	future := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	code, body := putJSONReq(t, url, f.token,
		`{"settings":{"reader.font":{"value":"x","updated_at":"`+future+`"}}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("future updated_at accepted: %d %v", code, body)
	}
	if body["code"] != errCodeTimeInFuture {
		t.Fatalf("want %s, got %v", errCodeTimeInFuture, body)
	}

	// Just inside the allowance is still fine: a client's clock being a
	// little ahead is ordinary, and refusing it would strand the device.
	soon := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if code, body = putJSONReq(t, url, f.token,
		`{"settings":{"reader.font":{"value":"x","updated_at":"`+soon+`"}}}`); code != http.StatusOK {
		t.Fatalf("slightly-ahead clock refused: %d %v", code, body)
	}

	ts := `"2026-06-01T12:00:00Z"`
	for name, put := range map[string]string{
		"nul in value":    `{"settings":{"reader.font":{"value":"a\u0000b","updated_at":` + ts + `}}}`,
		"nul in key":      `{"settings":{"a\u0000b":{"value":"x","updated_at":` + ts + `}}}`,
		"empty key":       `{"settings":{"":{"value":"x","updated_at":` + ts + `}}}`,
		"malformed time":  `{"settings":{"reader.font":{"value":"x","updated_at":"not-a-time"}}}`,
		"absent time":     `{"settings":{"reader.font":{"value":"x"}}}`,
		"empty settings":  `{"settings":{}}`,
		"null body":       `null`,
		"not json":        `{`,
		"oversized key":   `{"settings":{"` + strings.Repeat("k", 200) + `":{"value":"x","updated_at":` + ts + `}}}`,
		"oversized value": `{"settings":{"reader.font":{"value":"` + strings.Repeat("v", 5000) + `","updated_at":` + ts + `}}}`,
	} {
		code, body = putJSONReq(t, url, f.token, put)
		if code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %v", name, code, body)
		}
	}

	// None of the refusals stored anything beyond the one good write.
	code, body = getJSON(t, url, f.token)
	if code != http.StatusOK {
		t.Fatalf("get: %d %v", code, body)
	}
	if got := body["settings"].(map[string]any); len(got) != 1 {
		t.Fatalf("a refused put stored something: %v", got)
	}
}

// TestSettingsKeepsSubSecondPrecision pins the timestamp format. The
// store keeps the fraction it was given; emitting whole seconds would
// hand a client a timestamp strictly older than the stored one, so
// echoing it back could never satisfy the upsert's strict > and the key
// would look permanently un-writable.
func TestSettingsKeepsSubSecondPrecision(t *testing.T) {
	f := newFolderFixture(t)
	url := f.ts.URL + "/v1/me/settings"

	code, body := putJSONReq(t, url, f.token,
		`{"settings":{"k":{"value":"a","updated_at":"2026-06-01T12:00:00.5Z"}}}`)
	if code != http.StatusOK {
		t.Fatalf("put: %d %v", code, body)
	}
	got := body["settings"].(map[string]any)["k"].(map[string]any)["updated_at"].(string)
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("unparseable updated_at %q: %v", got, err)
	}
	want := time.Date(2026, 6, 1, 12, 0, 0, 500000000, time.UTC)
	if !parsed.Equal(want) {
		t.Fatalf("precision lost: got %s, want %s", got, want.Format(time.RFC3339Nano))
	}

	// Echoing the server's own timestamp back must not be treated as
	// newer, and must not lose the value either.
	code, body = putJSONReq(t, url, f.token,
		`{"settings":{"k":{"value":"b","updated_at":"`+got+`"}}}`)
	if code != http.StatusOK {
		t.Fatalf("echo put: %d %v", code, body)
	}
	if v := body["settings"].(map[string]any)["k"].(map[string]any)["value"]; v != "a" {
		t.Fatalf("an equal timestamp overwrote: %v", v)
	}
}

// TestSettingsNonUTCOffsetNormalises checks a client in a zone other
// than UTC is stored and compared at the same instant.
func TestSettingsNonUTCOffsetNormalises(t *testing.T) {
	f := newFolderFixture(t)
	url := f.ts.URL + "/v1/me/settings"

	code, body := putJSONReq(t, url, f.token,
		`{"settings":{"k":{"value":"a","updated_at":"2026-06-01T14:00:00+02:00"}}}`)
	if code != http.StatusOK {
		t.Fatalf("put: %d %v", code, body)
	}
	got := body["settings"].(map[string]any)["k"].(map[string]any)["updated_at"].(string)
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC); !parsed.Equal(want) {
		t.Fatalf("offset not normalised: %s", got)
	}

	// The same instant written as UTC must not count as newer.
	code, body = putJSONReq(t, url, f.token,
		`{"settings":{"k":{"value":"b","updated_at":"2026-06-01T12:00:00Z"}}}`)
	if code != http.StatusOK {
		t.Fatalf("put: %d %v", code, body)
	}
	if v := body["settings"].(map[string]any)["k"].(map[string]any)["value"]; v != "a" {
		t.Fatalf("same instant in another zone overwrote: %v", v)
	}
}
