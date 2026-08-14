package webui

import (
	"math"
	"net/http"
	"net/url"
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

// userCtx carries the signed-in user and everything the shell needs to
// draw itself: which palette to render, which browse view to use, which
// rail entry is the current one, and where a preference form should
// send the reader back to.
type userCtx struct {
	User    *store.User
	Prefs   prefs
	Section string
	Back    string
}

// uiCtx assembles that from a request, so a page does not have to think
// about it and cannot forget to.
func uiCtx(r *http.Request, u *store.User) userCtx {
	return userCtx{
		User:    u,
		Prefs:   readPrefs(r),
		Section: sectionOf(r.URL.Path),
		Back:    backTo(r.URL),
	}
}

// sectionOf names the rail entry a path belongs to. The rail is short
// and the paths are stable, so a switch says it more plainly than a
// table would.
func sectionOf(path string) string {
	rest := strings.TrimPrefix(strings.TrimPrefix(path, "/ui"), "/")
	head, _, _ := strings.Cut(rest, "/")
	switch head {
	case "":
		return "dashboard"
	case "library":
		return "library"
	// A single book or work is still the library, as far as the rail is
	// concerned: there is nowhere else those pages could belong.
	case "works", "books":
		return "library"
	case "devices", "settings", "admin":
		return head
	case "libraries":
		if strings.Contains(rest, "/search") {
			return "search"
		}
		return "browse"
	default:
		return head
	}
}

// backTo renders a URL as a path relative to the UI root, which is what
// a Location resolves against after a POST to /ui/preferences.
func backTo(u *url.URL) string {
	rest := strings.TrimPrefix(strings.TrimPrefix(u.Path, "/ui"), "/")
	if rest == "" {
		return "."
	}
	if u.RawQuery != "" {
		return rest + "?" + u.RawQuery
	}
	return rest
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

// plural counts things in words, because "1 books" reads as a bug even
// when the number is right.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// orDash is orPlaceholder for a table cell, where an empty string is
// not a missing title but a thing that has not happened yet.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orPlaceholder(s string) string {
	if s == "" {
		return "(untitled)"
	}
	return s
}

// pctClass rounds a progression to the nearest twentieth and names the
// stylesheet rule that draws it. A class rather than a style attribute:
// a per-card number is the one thing that would force
// style-src 'unsafe-inline' on a UI that does not need it, and five
// percentage points is finer than a 4mm bar can show anyway.
func pctClass(f float64) string {
	switch {
	case f <= 0:
		return "p0"
	case f >= 1:
		return "p100"
	}
	return "p" + strconv.Itoa(int(math.Round(f*20))*5)
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

// credit is how a list names a book's authors in one line. Three is
// where it stops: a card is one line of small type, and an anthology
// with nineteen contributors would push the title off it. The rest are
// counted rather than elided, because "and 16 others" says how much is
// missing where an ellipsis does not.
func credit(names []string) string {
	const shown = 3
	if len(names) <= shown {
		return strings.Join(names, ", ")
	}
	rest := len(names) - shown
	if rest == 1 {
		return strings.Join(names[:shown], ", ") + " and one other"
	}
	return strings.Join(names[:shown], ", ") +
		" and " + strconv.Itoa(rest) + " others"
}
