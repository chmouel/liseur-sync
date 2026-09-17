//go:build linux

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// TestSettingsRejectsNullValue pins the difference between a value that
// was left out, which is the empty string, and one sent as null, which
// is not part of the wire format. Decoding null straight into a string
// yields "" without an error, so without this a malformed client clears
// a preference and is told it succeeded.
func TestSettingsRejectsNullValue(t *testing.T) {
	f := newFolderFixture(t)
	url := f.ts.URL + "/v1/me/settings"

	code, _ := putJSONReq(t, url, f.token,
		`{"settings":{"k":{"value":"chosen","updated_at":"2026-06-01T12:00:00Z"}}}`)
	if code != http.StatusOK {
		t.Fatalf("seed: %d", code)
	}

	code, body := putJSONReq(t, url, f.token,
		`{"settings":{"k":{"value":null,"updated_at":"2026-06-02T12:00:00Z"}}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("null value accepted: %d %v", code, body)
	}

	code, body = getJSON(t, url, f.token)
	if code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	if v := body["settings"].(map[string]any)["k"].(map[string]any)["value"]; v != "chosen" {
		t.Fatalf("null cleared the value: %v", v)
	}

	// A value left out entirely is still the empty string.
	code, body = putJSONReq(t, url, f.token,
		`{"settings":{"empty":{"updated_at":"2026-06-02T12:00:00Z"}}}`)
	if code != http.StatusOK {
		t.Fatalf("absent value refused: %d %v", code, body)
	}
	if v := body["settings"].(map[string]any)["empty"].(map[string]any)["value"]; v != "" {
		t.Fatalf("absent value not empty: %v", v)
	}

	// A value of the wrong type is a client fault, not an empty string.
	code, body = putJSONReq(t, url, f.token,
		`{"settings":{"n":{"value":12,"updated_at":"2026-06-02T12:00:00Z"}}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("numeric value accepted: %d %v", code, body)
	}
}

// TestSettingsTimestampPrecisionIsMicroseconds pins the precision both
// backends can actually keep. PostgreSQL's TIMESTAMPTZ holds
// microseconds while SQLite holds whatever text it is handed, so a
// nanosecond kept on one and dropped on the other would make the same
// request mean two different things.
func TestSettingsTimestampPrecisionIsMicroseconds(t *testing.T) {
	f := newFolderFixture(t)
	url := f.ts.URL + "/v1/me/settings"

	code, body := putJSONReq(t, url, f.token,
		`{"settings":{"k":{"value":"a","updated_at":"2026-06-01T12:00:00.123456789Z"}}}`)
	if code != http.StatusOK {
		t.Fatalf("put: %d %v", code, body)
	}
	got := body["settings"].(map[string]any)["k"].(map[string]any)["updated_at"].(string)
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("unparseable updated_at %q: %v", got, err)
	}
	want := time.Date(2026, 6, 1, 12, 0, 0, 123456000, time.UTC)
	if !parsed.Equal(want) {
		t.Fatalf("not truncated to microseconds: got %s, want %s",
			got, want.Format(time.RFC3339Nano))
	}

	// Two writes inside one microsecond are the same instant, so the
	// second does not win. Saying otherwise would promise an ordering
	// the store cannot hold.
	code, body = putJSONReq(t, url, f.token,
		`{"settings":{"k":{"value":"b","updated_at":"2026-06-01T12:00:00.123456999Z"}}}`)
	if code != http.StatusOK {
		t.Fatalf("put: %d %v", code, body)
	}
	if v := body["settings"].(map[string]any)["k"].(map[string]any)["value"]; v != "a" {
		t.Fatalf("sub-microsecond difference counted as newer: %v", v)
	}
}

// TestSettingsBatchOrderIsDeterministic checks the rows of one request
// are always taken in the same order. Go map iteration is random, so
// without sorting two overlapping requests can reach the same rows in
// opposite orders, which PostgreSQL resolves by aborting one of them.
func TestSettingsBatchOrderIsDeterministic(t *testing.T) {
	f := newFolderFixture(t)
	url := f.ts.URL + "/v1/me/settings"

	// An invalid key placed among valid ones: whichever key is refused
	// first names itself in the error, so a stable error over many runs
	// is a stable order.
	const putBody = `{"settings":{
		"a":{"value":"1","updated_at":"2026-06-01T12:00:00Z"},
		"b":{"value":"2","updated_at":"not-a-time"},
		"c":{"value":"3","updated_at":"also-not-a-time"}
	}}`
	first := ""
	for i := 0; i < 20; i++ {
		code, body := putJSONReq(t, url, f.token, putBody)
		if code != http.StatusBadRequest {
			t.Fatalf("put: %d %v", code, body)
		}
		got, _ := body["error"].(string)
		if first == "" {
			first = got
		}
		if got != first {
			t.Fatalf("order varies between requests: %q then %q", first, got)
		}
	}
	if !strings.Contains(first, "key b") {
		t.Fatalf("keys not taken in sorted order: %q", first)
	}
}

func TestSettingsRejectsOversizedBatch(t *testing.T) {
	f := newFolderFixture(t)

	var b strings.Builder
	b.WriteString(`{"settings":{`)
	for i := 0; i <= f.srv.Cfg.Ops.SettingsMaxPerAccount; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"k%d":{"value":"v","updated_at":"2026-06-01T12:00:00Z"}`, i)
	}
	b.WriteString(`}}`)

	code, body := putJSONReq(t, f.ts.URL+"/v1/me/settings", f.token, b.String())
	if code != http.StatusBadRequest {
		t.Fatalf("want 400 for a batch larger than the account cap, got %d %v", code, body)
	}
}
