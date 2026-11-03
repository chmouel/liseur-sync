package webui

import (
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func TestDeviceTypeDetection(t *testing.T) {
	tests := []struct {
		name, deviceID, want string
	}{
		{"Chrome Browser", "dev-1", DeviceWeb},
		{"My Android Phone", "dev-2", DeviceMobile},
		{"Boox Palma", "dev-3", DeviceEReader},
		{"Kobo Clara HD", "dev-4", DeviceEReader},
		{"Kindle Paperwhite", "dev-5", DeviceEReader},
		{"KOReader-Kobo", "dev-6", DeviceEReader},
		{"MacBook Pro CLI", "dev-7", DeviceDesktop},
	}

	for _, tt := range tests {
		got := detectDeviceType(tt.name, tt.deviceID)
		if got != tt.want {
			t.Errorf("detectDeviceType(%q, %q) = %q, want %q", tt.name, tt.deviceID, got, tt.want)
		}
	}
}

func TestSortTokensByDeviceType(t *testing.T) {
	now := time.Now()
	toks := []store.Token{
		{ID: "t1", Name: "MacBook CLI", DeviceID: "d1"},
		{ID: "t2", Name: "Chrome Web Reader", DeviceID: "d2"},
		{ID: "t3", Name: "Android Mobile", DeviceID: "d3"},
		{ID: "t4", Name: "Kobo Clara E-Reader", DeviceID: "d4"},
		{ID: "t5", Name: "Revoked Web Reader", DeviceID: "d5", RevokedAt: &now},
	}

	sortTokens(toks)

	wantIDs := []string{"t2", "t3", "t4", "t1", "t5"}
	for i, want := range wantIDs {
		if toks[i].ID != want {
			t.Errorf("toks[%d].ID = %q, want %q", i, toks[i].ID, want)
		}
	}
}

// TestActiveFiltersKeepOnlyLiveCredentials: the filters behind the
// Devices page and the admin's per-user page, which list what an account
// can still use rather than every row it ever had.
func TestActiveFiltersKeepOnlyLiveCredentials(t *testing.T) {
	now := time.Now()
	toks := activeTokens([]store.Token{
		{ID: "a"}, {ID: "b", RevokedAt: &now}, {ID: "c"},
	})
	if len(toks) != 2 || toks[0].ID != "a" || toks[1].ID != "c" {
		t.Fatalf("activeTokens = %+v", toks)
	}
	kos := activeKosyncDevices([]store.KosyncDevice{
		{DeviceSlot: "a"}, {DeviceSlot: "b", RevokedAt: &now},
	})
	if len(kos) != 1 || kos[0].DeviceSlot != "a" {
		t.Fatalf("activeKosyncDevices = %+v", kos)
	}
	kop := activeKopluginDevices([]store.KopluginDevice{
		{ID: "a"}, {ID: "b", RevokedAt: &now},
	})
	if len(kop) != 1 || kop[0].ID != "a" {
		t.Fatalf("activeKopluginDevices = %+v", kop)
	}
	if got := activeTokens(nil); got == nil || len(got) != 0 {
		t.Fatalf("activeTokens(nil) = %+v, want an empty slice", got)
	}
}

// TestSplitReaderTokensDropsSignedOutBrowsers: a browser whose reader
// credentials were all revoked has signed out, so it is not one of the
// browsers reading here and the page has no row to show for it.
func TestSplitReaderTokensDropsSignedOutBrowsers(t *testing.T) {
	now := time.Now()
	mine, browsers := splitReaderTokens([]store.Token{
		{ID: "reader-live", Name: auth.ReaderTokenName, DeviceID: "live"},
		{ID: "reader-dead", Name: auth.ReaderTokenName, DeviceID: "dead", RevokedAt: &now},
		{ID: "api", Name: "Boox", DeviceID: "d1"},
	})
	if len(mine) != 1 || mine[0].ID != "api" {
		t.Fatalf("mine = %+v", mine)
	}
	if len(browsers) != 1 || browsers[0].DeviceID != "live" {
		t.Fatalf("browsers = %+v", browsers)
	}
}

func TestCompactDeviceID(t *testing.T) {
	const id = "koplugin:012345678901234567890"

	got := compactDeviceID(id)
	if got != "koplugin:…34567890" {
		t.Fatalf("compactDeviceID(%q) = %q", id, got)
	}
	if compactDeviceID("short-id") != "short-id" {
		t.Fatal("short device IDs should not be changed")
	}
}
