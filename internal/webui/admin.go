package webui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/buildinfo"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// configFacts is the configuration as the overview shows it: an
// explicit list of named fields, built by hand.
//
// Hand-written rather than reflected over the config struct on purpose
// (ADR-0013). A walk over the struct would put every field a future
// commit adds onto a web page for free, including the next secret; this
// way a new setting is invisible until somebody decides it should be
// visible. `database.url` is the field that makes the point: it carries
// the PostgreSQL password and is never rendered, while the driver is.
type configFacts struct {
	Fields []configFact
}

type configFact struct {
	Label string
	Value string
}

func describeConfig(c config.Config) configFacts {
	f := configFacts{}
	add := func(label, value string) {
		f.Fields = append(f.Fields, configFact{Label: label, Value: value})
	}
	add("Listen address", c.ListenAddr)
	add("Database driver", c.Database.Driver)
	add("Content root", c.Content.Root)
	add("Maximum upload", humanBytes(c.Content.MaxUploadBytes))
	add("Staging limit", humanBytesOr(c.Content.MaxStagingBytes, "unlimited"))
	add("Per-account quota", humanBytesOr(c.Content.QuotaBytes, "unlimited"))
	add("Trash retention", hours(c.Content.TrashRetentionHours))
	add("Failed upload retention", hours(c.Content.FailureRetentionHours))
	add("Orphan blob grace", hours(c.Content.OrphanGraceHours))
	add("Ingest worker interval", seconds(c.Content.IngestWorkerInterval))
	add("Library refresh tick", secondsOr(c.Content.RefreshTick, "never"))
	add("Op log retention", plural(c.Ops.RetentionDays, "day"))
	add("Op log compaction", onOff(c.Ops.CompactionEnabled))
	add("kosync adapter", onOff(c.Adapters.Kosync))
	add("koplugin adapter", onOff(c.Adapters.Koplugin))
	add("Open registration", onOff(c.OpenRegistration))
	add("Credentials over plain HTTP", onOff(c.InsecureHTTP))
	add("Trusted proxies", listOr(c.TrustedProxies, "none"))
	add("CORS origins", listOr(c.CORSAllowedOrigins, "none"))
	add("Reader origin", orValue(c.ReaderOrigin, "same origin"))
	add("Metadata providers", listOr(c.Metadata.Providers, "none (external lookup off)"))
	return f
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func hours(h int) string {
	switch {
	case h <= 0:
		return "off"
	case h%24 == 0:
		return plural(h/24, "day")
	default:
		return plural(h, "hour")
	}
}

func seconds(s int) string { return plural(s, "second") }

func secondsOr(s int, zero string) string {
	if s <= 0 {
		return zero
	}
	return seconds(s)
}

func listOr(v []string, empty string) string {
	if len(v) == 0 {
		return empty
	}
	return strings.Join(v, ", ")
}

func orValue(s, empty string) string {
	if s == "" {
		return empty
	}
	return s
}

func humanBytesOr(n int64, zero string) string {
	if n <= 0 {
		return zero
	}
	return humanBytes(n)
}

// humanBytes renders a byte count the way an operator reads one. Binary
// units, because that is what the limits are written in.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

func (s *Server) handleAdminOverview(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	counts, err := s.St.AdminCounts(r.Context())
	if err != nil {
		http.Error(w, "counts unavailable", http.StatusInternalServerError)
		return
	}
	adminPage("Admin", relPrefix(r.URL.Path), uiCtx(r, u), csrfFor(a), "overview",
		adminOverviewBody(relPrefix(r.URL.Path), counts, buildinfo.Get(),
			describeConfig(s.Cfg))).
		Render(r.Context(), w)
}

// inviteTTL is how long an invite code is worth typing. A week is long
// enough to reach somebody by whatever channel is convenient and short
// enough that a forgotten code is not a standing account.
const inviteTTL = 7 * 24 * time.Hour
