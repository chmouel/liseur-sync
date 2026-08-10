package webui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/chmouel/liseur-sync/internal/store"
)

// relPrefix returns the relative path ("", "../", "../../", ...) that
// takes a page served at request path p back to the UI root (/ui/).
// All links, form actions and redirects are rendered relative to it so
// the UI works when a reverse proxy exposes it under a stripped
// subpath (e.g. Caddy `handle_path /sync*`): the browser resolves
// relative URLs against its own (unstripped) URL.
func relPrefix(p string) string {
	if !strings.HasSuffix(p, "/") {
		p = p[:strings.LastIndex(p, "/")+1]
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/ui/"), "/")
	if rest == "" {
		return ""
	}
	return strings.Repeat("../", strings.Count(rest, "/")+1)
}

// userCtx carries the signed-in user into templates.
type userCtx struct {
	User *store.User
}

// SessionRow is one recent session for the dashboard.
type SessionRow struct {
	When      string
	WorkID    string
	WorkTitle string
	DeviceID  string
	Minutes   int
	StartProg float64
	EndProg   float64
}

func pct(f float64) string { return strconv.Itoa(int(f*100)) + "%" }
func f0(f float64) string  { return strconv.FormatFloat(f, 'f', 0, 64) }
func i2s(i int) string     { return strconv.Itoa(i) }

func orPlaceholder(s string) string {
	if s == "" {
		return "(untitled)"
	}
	return s
}

func barWidth(f float64) string {
	return fmt.Sprintf("width:%d%%", int(f*100))
}

// cellClass buckets minutes for the heatmap.
func cellClass(minutes float64) string {
	switch {
	case minutes <= 0:
		return "cell c0"
	case minutes < 15:
		return "cell c1"
	case minutes < 45:
		return "cell c2"
	case minutes < 90:
		return "cell c3"
	default:
		return "cell c4"
	}
}
