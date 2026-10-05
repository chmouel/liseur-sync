package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// settingsFuture is how far ahead of the server's clock a client's
// updated_at may sit before the write is refused. The timestamp stays
// client-assigned so a retried request that arrives late cannot undo a
// newer write from the same device. That trust needs a bound: the upsert
// keeps whichever side is newer, so a single write stamped years ahead
// would pin the key forever and no later write could ever move it
// again, with no delete route to recover. A day is the same allowance
// POST /v1/ops gives client_ts.
const settingsFuture = 24 * time.Hour

// settingsScope tells a client whose settings it is looking at. A server
// that predates ADR-0050 answers without it, and its map is the whole
// account's, so a client that keeps settings per device must not adopt
// anything from a response that lacks it.
const settingsScope = "device"

func writeSettings(w http.ResponseWriter, settings map[string]any) {
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "scope": settingsScope})
}

// HandleGetSettings answers with the calling device's own settings. The
// device is the token's, so two devices of one account never see each
// other's (ADR-0050).
func (s *Server) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)
	out, err := s.settingsSnapshot(r.Context(), tok.UserID, tok.DeviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings read failed")
		return
	}
	writeSettings(w, out)
}

func (s *Server) HandlePutSettings(w http.ResponseWriter, r *http.Request) {
	tok, _ := auth.TokenFrom(r)

	var body struct {
		Settings map[string]struct {
			// RawMessage rather than string: an absent value is
			// deliberately the empty one, but an explicit null is a
			// client sending something it should not, and decoding
			// straight into a string makes the two indistinguishable
			// while quietly clearing a preference.
			Value     json.RawMessage `json:"value"`
			UpdatedAt string          `json:"updated_at"`
		} `json:"settings"`
	}
	max := s.Cfg.Ops.MaxBodyBytes
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Decode stops at the end of the first value, so without this a
	// second document, trailing rubbish, or padding past the body limit
	// would be stored and answered 200 as if the request were clean.
	if _, err := dec.Token(); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Settings) == 0 {
		writeError(w, http.StatusBadRequest, "no settings provided")
		return
	}
	// Refused before the store is opened, not inside it. More keys than
	// an account may hold can never succeed, and letting the request
	// through means probing every one of them while holding the
	// account's lock, which is a cheap way to stall that account.
	if len(body.Settings) > s.Cfg.Ops.SettingsMaxPerAccount {
		writeError(w, http.StatusBadRequest, "too many settings in one request")
		return
	}

	// Map iteration order is random, so without this two overlapping
	// requests can reach the same rows in opposite orders.
	keys := make([]string, 0, len(body.Settings))
	for key := range body.Settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	now := time.Now()
	settings := make([]store.DeviceSetting, 0, len(body.Settings))
	for _, key := range keys {
		v := body.Settings[key]
		value, err := settingValue(v.Value)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error()+" for key "+key)
			return
		}
		if err := s.checkSettingKey(key, value); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		t, err := time.Parse(time.RFC3339, v.UpdatedAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid updated_at for key "+key)
			return
		}
		if t.After(now.Add(settingsFuture)) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "updated_at in the future for key " + key,
				"code":  errCodeTimeInFuture,
			})
			return
		}
		settings = append(settings, store.DeviceSetting{
			Key:   key,
			Value: value,
			// Truncated here rather than left to the backend. Postgres
			// TIMESTAMPTZ holds microseconds and SQLite holds whatever
			// text it is given, so without this the same request means
			// two different things on the two backends, and the finer
			// one promises a precision the other cannot keep.
			UpdatedAt: t.Truncate(time.Microsecond),
		})
	}

	if err := s.St.PutDeviceSettings(r.Context(), tok.UserID, tok.DeviceID, settings, s.Cfg.Ops.SettingsMaxPerAccount); err != nil {
		if errors.Is(err, store.ErrQuotaExceeded) {
			writeError(w, http.StatusConflict, "too many settings for this device")
			return
		}
		writeError(w, http.StatusInternalServerError, "settings write failed")
		return
	}

	// The upsert keeps whichever side is newer, so a pair that lost is
	// silently not stored and the client cannot tell from the status
	// code alone. Returning the merged state is what lets it find out,
	// which makes this read part of the write's contract rather than a
	// convenience.
	out, err := s.settingsSnapshot(r.Context(), tok.UserID, tok.DeviceID)
	if err != nil {
		// The write committed; only reading it back failed. Say so,
		// rather than reporting a write failure that did not happen.
		writeError(w, http.StatusInternalServerError, "settings stored but could not be read back")
		return
	}
	writeSettings(w, out)
}

func (s *Server) settingsSnapshot(ctx context.Context, userID, deviceID string) (map[string]any, error) {
	settings, err := s.St.GetDeviceSettings(ctx, userID, deviceID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(settings))
	for _, us := range settings {
		out[us.Key] = map[string]any{
			"value": us.Value,
			// RFC3339Nano, not RFC3339: the store keeps the fraction it
			// was given, and truncating it here would hand the client a
			// timestamp strictly older than the stored one. Echoing that
			// back could then never satisfy the upsert's strict >, so a
			// key would look permanently un-writable.
			"updated_at": us.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	return out, nil
}

// settingValue reads a setting's value, telling an omitted field apart
// from an explicit null. Omitted means the empty string, which is a
// legitimate value a client may want to store. Null is not part of the
// wire format, and accepting it would let a malformed request clear a
// preference while answering 200.
func settingValue(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	if string(raw) == "null" {
		return "", errors.New("settings value may not be null")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", errors.New("settings value must be a string")
	}
	return value, nil
}

func (s *Server) checkSettingKey(key, value string) error {
	if key == "" {
		return errors.New("empty settings key")
	}
	if len(key) > s.Cfg.Ops.SettingsMaxKeyBytes {
		return errors.New("settings key too long: " + key)
	}
	if len(value) > s.Cfg.Ops.SettingsMaxValueBytes {
		return errors.New("settings value too long for key " + key)
	}
	// Postgres refuses a NUL byte in a TEXT column while SQLite stores
	// it, so without this the same request is a 500 on one backend and a
	// success on the other. Invalid UTF-8 needs no check here: it cannot
	// survive JSON decoding, which replaces it with U+FFFD.
	if strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
		return errors.New("settings key or value contains a character that cannot be stored")
	}
	return nil
}
