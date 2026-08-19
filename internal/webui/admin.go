package webui

import (
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
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
	add("Cache directory", c.Content.CacheDir)
	add("Scan file ceiling", i2s(c.Content.ScanMaxFiles))
	add("Scan depth ceiling", i2s(c.Content.ScanMaxDepth))
	add("Op log retention", plural(c.Ops.RetentionDays, "day"))
	add("Op log compaction", onOff(c.Ops.CompactionEnabled))
	add("kosync adapter", onOff(c.Adapters.Kosync))
	add("koplugin adapter", onOff(c.Adapters.Koplugin))
	add("Open registration", onOff(c.OpenRegistration))
	add("Credentials over plain HTTP", onOff(c.InsecureHTTP))
	add("Trusted proxies", listOr(c.TrustedProxies, "none"))
	add("CORS origins", listOr(c.CORSAllowedOrigins, "none"))
	add("Reader origin", orValue(c.ReaderOrigin, "same origin"))
	add("Folder roots for the panel", listOr(c.Content.FolderRoots,
		"anywhere the server can read"))
	return f
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
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

// inviteTTL is how long an invite code is worth typing. A week is long
// enough to reach somebody by whatever channel is convenient and short
// enough that a forgotten code is not a standing account.
const inviteTTL = 7 * 24 * time.Hour
