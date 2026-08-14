package webui

import (
	"sort"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

const (
	DeviceWeb     = "web"
	DeviceMobile  = "mobile"
	DeviceEReader = "ereader"
	DeviceDesktop = "desktop"
)

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
