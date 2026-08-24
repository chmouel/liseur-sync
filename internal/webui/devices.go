package webui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

const (
	DeviceWeb     = "web"
	DeviceMobile  = "mobile"
	DeviceEReader = "ereader"
	DeviceDesktop = "desktop"
)

// deviceLabels returns the names a reader chose for their devices, keyed by
// the device id written into native sessions. The adapter ids are namespaced
// so the three credential types can share this small lookup.
func deviceLabels(ctx context.Context, st store.Store, userID string) map[string]string {
	labels := map[string]string{}
	if tokens, err := st.ListTokens(ctx, userID); err == nil {
		for _, token := range tokens {
			if token.Name != "" {
				labels[token.DeviceID] = token.Name
			}
		}
	}
	if devices, err := st.ListKosyncDevices(ctx, userID); err == nil {
		for _, device := range devices {
			if device.Label != "" {
				labels["kosync:"+device.DeviceSlot] = device.Label
			}
		}
	}
	if devices, err := st.ListKopluginDevices(ctx, userID); err == nil {
		for _, device := range devices {
			if device.Label != "" {
				labels[device.DeviceID] = device.Label
			}
		}
	}
	return labels
}

const compactDeviceIDLimit = 20

func compactDeviceID(id string) string {
	runes := []rune(id)
	if len(runes) <= compactDeviceIDLimit {
		return id
	}
	return string(runes[:9]) + "…" + string(runes[len(runes)-8:])
}

func detectDeviceType(name, deviceID string) string {
	s := strings.ToLower(name + " " + deviceID)
	switch {
	case strings.Contains(s, "web") || strings.Contains(s, "browser") || strings.Contains(s, "chrome") || strings.Contains(s, "firefox") || strings.Contains(s, "safari") || strings.Contains(s, "edge"):
		return DeviceWeb
	case strings.Contains(s, "kobo") || strings.Contains(s, "kindle") || strings.Contains(s, "onyx") || strings.Contains(s, "boox") || strings.Contains(s, "pocketbook") || strings.Contains(s, "koreader") || strings.Contains(s, "kosync") || strings.Contains(s, "koplugin") || strings.Contains(s, "ereader") || strings.Contains(s, "e-reader"):
		return DeviceEReader
	case strings.Contains(s, "android") || strings.Contains(s, "phone") || strings.Contains(s, "iphone") || strings.Contains(s, "palma") || strings.Contains(s, "pixel") || strings.Contains(s, "galaxy") || strings.Contains(s, "mobile") || strings.Contains(s, "ios") || strings.Contains(s, "samsung") || strings.Contains(s, "liseur"):
		return DeviceMobile
	default:
		return DeviceDesktop
	}
}

func deviceTypeRank(dt string) int {
	switch dt {
	case DeviceWeb:
		return 1
	case DeviceMobile:
		return 2
	case DeviceEReader:
		return 3
	default:
		return 4
	}
}

func deviceTypeLabel(dt string) string {
	switch dt {
	case DeviceWeb:
		return "Web Browser"
	case DeviceMobile:
		return "Mobile"
	case DeviceEReader:
		return "E-Reader"
	default:
		return "Desktop / App"
	}
}

func sortTokens(toks []store.Token) {
	sort.SliceStable(toks, func(i, j int) bool {
		iActive, jActive := toks[i].RevokedAt == nil, toks[j].RevokedAt == nil
		if iActive != jActive {
			return iActive
		}
		ti := deviceTypeRank(detectDeviceType(toks[i].Name, toks[i].DeviceID))
		tj := deviceTypeRank(detectDeviceType(toks[j].Name, toks[j].DeviceID))
		if ti != tj {
			return ti < tj
		}
		return strings.ToLower(toks[i].Name) < strings.ToLower(toks[j].Name)
	})
}

func sortKosyncDevices(devs []store.KosyncDevice) {
	sort.SliceStable(devs, func(i, j int) bool {
		iActive, jActive := devs[i].RevokedAt == nil, devs[j].RevokedAt == nil
		if iActive != jActive {
			return iActive
		}
		ti := deviceTypeRank(detectDeviceType(devs[i].Label, devs[i].DeviceSlot))
		tj := deviceTypeRank(detectDeviceType(devs[j].Label, devs[j].DeviceSlot))
		if ti != tj {
			return ti < tj
		}
		return strings.ToLower(devs[i].Label) < strings.ToLower(devs[j].Label)
	})
}

func sortKopluginDevices(devs []store.KopluginDevice) {
	sort.SliceStable(devs, func(i, j int) bool {
		iActive, jActive := devs[i].RevokedAt == nil, devs[j].RevokedAt == nil
		if iActive != jActive {
			return iActive
		}
		ti := deviceTypeRank(detectDeviceType(devs[i].Label, devs[i].DeviceID))
		tj := deviceTypeRank(detectDeviceType(devs[j].Label, devs[j].DeviceID))
		if ti != tj {
			return ti < tj
		}
		return strings.ToLower(devs[i].Label) < strings.ToLower(devs[j].Label)
	})
}

// browserSession is one browser that has read here, as the Devices page
// shows it: not a credential, since the reader's credential lasts an
// hour and is replaced without anyone asking, but the browser behind
// however many of them there have been.
type browserSession struct {
	DeviceID string
	LastUsed time.Time
	Active   bool
}

// splitReaderTokens separates the credentials a person made from the
// ones the server issued to itself.
//
// The reader mints a token on every open and again every hour. Listing
// those beside a Boox's token, with a Revoke link and scope checkboxes,
// says they are the same kind of thing and invites managing something
// that will be replaced before the page is closed. So they are counted
// as browsers instead — one row per device id, however many tokens that
// took — and the table keeps only what somebody chose to create.
func splitReaderTokens(toks []store.Token) (mine []store.Token, browsers []browserSession) {
	byDevice := map[string]browserSession{}
	for _, t := range toks {
		if t.Name != auth.ReaderTokenName {
			mine = append(mine, t)
			continue
		}
		b := byDevice[t.DeviceID]
		b.DeviceID = t.DeviceID
		if t.RevokedAt == nil {
			b.Active = true
		}
		when := t.CreatedAt
		if t.LastUsed != nil && t.LastUsed.After(when) {
			when = *t.LastUsed
		}
		if when.After(b.LastUsed) {
			b.LastUsed = when
		}
		byDevice[t.DeviceID] = b
	}
	for _, b := range byDevice {
		browsers = append(browsers, b)
	}
	sort.Slice(browsers, func(i, j int) bool {
		if browsers[i].LastUsed.Equal(browsers[j].LastUsed) {
			return browsers[i].DeviceID < browsers[j].DeviceID
		}
		return browsers[i].LastUsed.After(browsers[j].LastUsed)
	})
	return mine, browsers
}
