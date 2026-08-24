package webui

import (
	"testing"
	"time"

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
