package kosync

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
)

func CrossPointPlugin(server, slot, key string) ([]byte, error) {
	config, err := json.MarshalIndent(map[string]string{"server": server, "username": slot, "key": key}, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(config) > 2048 {
		return nil, errors.New("plugin configuration exceeds CrossPoint's 2KB limit")
	}
	body, _ := json.Marshal(sessionEvent{
		EventID: "{event.id}", Document: "{event.document}", Start: "{event.start_time}", End: "{event.end_time}",
		Emitted: "{event.ts}", Duration: "{event.duration_seconds}", StartProgress: "{event.start_progress_bp}",
		EndProgress: "{event.end_progress_bp}", Scale: "{event.progress_scale}",
	})
	manifest, err := json.MarshalIndent(map[string]any{
		"title":       "Liseur reading statistics",
		"description": "Send measured reader.session events to Liseur Sync",
		"config":      map[string]string{"file": "config.json"},
		"events": map[string]any{"reader.session": map[string]any{
			"connect": true,
			"request": map[string]any{
				"method": "POST", "url": "{cfg.server}/adapter/kosync/sessions",
				"headers": map[string]string{"Content-Type": "application/json", "x-auth-user": "{cfg.username}", "x-auth-key": "{cfg.key}"},
				"body":    string(body),
			},
		}},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	readme := []byte(`Liseur Sync reading statistics for CrossPoint

Requires firmware with the reader.session plugin event (not just reader.exit).
Copy the liseur-stats directory into /plugins on your SD card.
Enable Settings > Connectivity > Plugins, refresh/restart plugins, then close
and reopen your book to start a fresh session after activation.
Keep ordinary KoSync progress sync enabled, with Binary document matching.
Use the original EPUB downloaded from Liseur Sync; do not Optimize EPUB.

config.json contains your device's password-equivalent pairing credential.
Keep this ZIP and that file private. Revoking the paired slot on the Devices
page revokes both progress and statistics uploads.

Downloading this bundle enables measured statistics for this slot permanently.
Older inferred history is kept. Progress received after activation is no longer
estimated as reading time. Sessions starting before activation are acknowledged
but ignored. Re-downloading does not move the activation time. An absent or
disabled plugin does not switch the slot back to inferred time.

Only counted forward-page dwell contributes: at least 2 seconds, at most
30 minutes per dwell; a summary needs at least 10 seconds. Backward turns,
chapter skips and failed turns do not count. This is not total time awake.
The event's actual emission time determines the account-local statistics day.

Events upload when online. Sleep may connect using saved Wi-Fi when battery
is at least 20%, within the firmware's connection deadline. Failed uploads
remain queued, but CrossPoint's 4KB per-plugin outbox can be dropped when full.
Offline statistics delivery is therefore best effort.
`)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range []struct {
		name string
		data []byte
	}{{"device.json", manifest}, {"config.json", config}, {"README.txt", readme}} {
		header := &zip.FileHeader{Name: "liseur-stats/" + file.name, Method: zip.Deflate}
		header.SetMode(0600)
		w, err := zw.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
