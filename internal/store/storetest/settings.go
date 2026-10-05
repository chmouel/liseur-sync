package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/store"
)

// settingsCap is a cap generous enough not to interfere with the cases
// that are not about the cap.
const settingsCap = 256

// settingsDevice is the device the single-device cases write as.
const settingsDevice = "settings-phone"

func testDeviceSettings(t *testing.T, open OpenFunc) {
	s := open(t)
	ctx := t.Context()
	alice := MkUser(t, s, "settings-alice")

	// Empty on a fresh device.
	got, err := s.GetDeviceSettings(ctx, alice.ID, settingsDevice)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("fresh device has settings: %v", got)
	}

	t1 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

	// Put two keys.
	if err := s.PutDeviceSettings(ctx, alice.ID, settingsDevice, []store.DeviceSetting{
		{Key: "reader.font", Value: "literata", UpdatedAt: t1},
		{Key: "reader.theme", Value: "sepia", UpdatedAt: t1},
	}, settingsCap); err != nil {
		t.Fatal(err)
	}

	got, err = s.GetDeviceSettings(ctx, alice.ID, settingsDevice)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 settings, got %d: %v", len(got), got)
	}
	if got[0].Key != "reader.font" || got[0].Value != "literata" {
		t.Fatalf("first setting: %+v", got[0])
	}
	if got[1].Key != "reader.theme" || got[1].Value != "sepia" {
		t.Fatalf("second setting: %+v", got[1])
	}

	// Last-writer-wins: a newer timestamp overwrites.
	if err := s.PutDeviceSettings(ctx, alice.ID, settingsDevice, []store.DeviceSetting{
		{Key: "reader.font", Value: "vollkorn", UpdatedAt: t2},
	}, settingsCap); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetDeviceSettings(ctx, alice.ID, settingsDevice)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]store.DeviceSetting{}
	for _, us := range got {
		byKey[us.Key] = us
	}
	if byKey["reader.font"].Value != "vollkorn" {
		t.Fatalf("newer write lost: %+v", byKey["reader.font"])
	}
	if byKey["reader.theme"].Value != "sepia" {
		t.Fatalf("untouched key changed: %+v", byKey["reader.theme"])
	}

	// Stale write is silently dropped.
	if err := s.PutDeviceSettings(ctx, alice.ID, settingsDevice, []store.DeviceSetting{
		{Key: "reader.font", Value: "inter", UpdatedAt: t1},
	}, settingsCap); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetDeviceSettings(ctx, alice.ID, settingsDevice)
	if err != nil {
		t.Fatal(err)
	}
	byKey = map[string]store.DeviceSetting{}
	for _, us := range got {
		byKey[us.Key] = us
	}
	if byKey["reader.font"].Value != "vollkorn" {
		t.Fatalf("stale write overwrote: %+v", byKey["reader.font"])
	}

	// Empty put is a no-op.
	if err := s.PutDeviceSettings(ctx, alice.ID, settingsDevice, nil, settingsCap); err != nil {
		t.Fatal(err)
	}

	// Cross-user isolation.
	bob := MkUser(t, s, "settings-bob")
	bobs, err := s.GetDeviceSettings(ctx, bob.ID, settingsDevice)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobs) != 0 {
		t.Fatalf("bob sees alice's settings: %v", bobs)
	}

	// Device isolation: another device of the same account sees none
	// of alice's phone settings, and its own writes stay its own, even
	// under the same key.
	tablet := "settings-tablet"
	tabs, err := s.GetDeviceSettings(ctx, alice.ID, tablet)
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 0 {
		t.Fatalf("tablet sees the phone's settings: %v", tabs)
	}
	if err := s.PutDeviceSettings(ctx, alice.ID, tablet, []store.DeviceSetting{
		{Key: "reader.font", Value: "inter", UpdatedAt: t2.Add(time.Hour)},
	}, settingsCap); err != nil {
		t.Fatal(err)
	}
	tabs, err = s.GetDeviceSettings(ctx, alice.ID, tablet)
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 1 || tabs[0].Key != "reader.font" || tabs[0].Value != "inter" {
		t.Fatalf("tablet settings: %v", tabs)
	}
	got, err = s.GetDeviceSettings(ctx, alice.ID, settingsDevice)
	if err != nil {
		t.Fatal(err)
	}
	byKey = map[string]store.DeviceSetting{}
	for _, us := range got {
		byKey[us.Key] = us
	}
	if byKey["reader.font"].Value != "vollkorn" {
		t.Fatalf("tablet's newer write reached the phone: %+v", byKey["reader.font"])
	}

	// Sub-second precision survives the round trip. The upsert keeps
	// whichever side is newer under a strict >, so a stored fraction
	// that got truncated on the way out would hand a client a timestamp
	// it could never satisfy. Microseconds rather than nanoseconds:
	// SQLite keeps the full fraction as TEXT while Postgres TIMESTAMPTZ
	// truncates below a microsecond, and this asserts what both backends
	// promise.
	frac := time.Date(2026, 6, 3, 12, 0, 0, 500000000, time.UTC)
	if err := s.PutDeviceSettings(ctx, alice.ID, settingsDevice, []store.DeviceSetting{
		{Key: "reader.fraction", Value: "x", UpdatedAt: frac},
	}, settingsCap); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetDeviceSettings(ctx, alice.ID, settingsDevice)
	if err != nil {
		t.Fatal(err)
	}
	byKey = map[string]store.DeviceSetting{}
	for _, us := range got {
		byKey[us.Key] = us
	}
	if stored := byKey["reader.fraction"].UpdatedAt.UTC(); !stored.Equal(frac) {
		t.Fatalf("fraction lost: stored %s, want %s",
			stored.Format(time.RFC3339Nano), frac.Format(time.RFC3339Nano))
	}

	// The per-device cap counts new keys only: replacing a key already
	// stored is not growth, and must still be allowed at the cap.
	carol := MkUser(t, s, "settings-carol")
	if err := s.PutDeviceSettings(ctx, carol.ID, settingsDevice, []store.DeviceSetting{
		{Key: "a", Value: "1", UpdatedAt: t1},
		{Key: "b", Value: "1", UpdatedAt: t1},
	}, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDeviceSettings(ctx, carol.ID, settingsDevice, []store.DeviceSetting{
		{Key: "b", Value: "2", UpdatedAt: t2},
	}, 2); err != nil {
		t.Fatalf("replacing a key at the cap was refused: %v", err)
	}
	if err := s.PutDeviceSettings(ctx, carol.ID, settingsDevice, []store.DeviceSetting{
		{Key: "c", Value: "1", UpdatedAt: t2},
	}, 2); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("want ErrQuotaExceeded past the cap, got %v", err)
	}
	// A refused put stores nothing, including the pairs it could have
	// taken: the whole batch shares one transaction.
	if err := s.PutDeviceSettings(ctx, carol.ID, settingsDevice, []store.DeviceSetting{
		{Key: "b", Value: "3", UpdatedAt: t2.Add(time.Hour)},
		{Key: "d", Value: "1", UpdatedAt: t2},
	}, 2); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
	// The cap is per device: another device of a full account still
	// has room of its own.
	if err := s.PutDeviceSettings(ctx, carol.ID, "settings-tablet", []store.DeviceSetting{
		{Key: "c", Value: "1", UpdatedAt: t2},
	}, 2); err != nil {
		t.Fatalf("a second device was refused by the first one's cap: %v", err)
	}
	got, err = s.GetDeviceSettings(ctx, carol.ID, settingsDevice)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("refused put changed the key set: %v", got)
	}
	for _, us := range got {
		if us.Key == "b" && us.Value != "2" {
			t.Fatalf("refused put still wrote: %+v", us)
		}
	}
}
