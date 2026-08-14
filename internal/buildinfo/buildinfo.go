// Package buildinfo reports which build of this server is running.
//
// The answer matters on exactly the deployment least able to find out
// any other way: a container somebody pulled months ago, whose operator
// is on the overview page asking whether a bug they read about is one
// they have. So the version is stamped at link time where it is known
// (`-ldflags -X`, in both the binary workflow and .ko.yaml), and where
// it is not, it falls back to what the Go toolchain already embedded —
// the module version for `go install`, or the VCS revision recorded for
// a plain `go build` in a work tree.
package buildinfo

import (
	"runtime/debug"
	"sync"
)

// Version is the release string, set with
// `-ldflags "-X github.com/chmouel/liseur-sync/internal/buildinfo.Version=v1.2.3"`.
// Empty means "not stamped", and the fallbacks below apply.
var Version string

// Revision is the source revision, stamped the same way. Empty falls
// back to the VCS revision the toolchain embeds.
var Revision string

// Info is what a page shows: a few strings, any of which may be empty
// or "dev", and none of which is ever a secret.
type Info struct {
	Version  string // "v1.2.3", a bare revision, or "dev"
	Revision string // full VCS revision when known
	Modified bool   // the work tree had uncommitted changes
	Go       string // toolchain that built it
}

var read = sync.OnceValue(func() Info {
	i := Info{Version: Version, Revision: Revision}
	if bi, ok := debug.ReadBuildInfo(); ok {
		i.Go = bi.GoVersion
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if i.Revision == "" {
					i.Revision = s.Value
				}
			case "vcs.modified":
				i.Modified = s.Value == "true"
			}
		}
		if i.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			i.Version = bi.Main.Version
		}
	}
	if i.Version == "" {
		if i.Revision != "" {
			i.Version = shortRevision(i.Revision)
		} else {
			i.Version = "dev"
		}
	}
	return i
})

// Get returns the build information, computed once.
func Get() Info { return read() }

// Short is the one-line form for a page header: the version, marked
// when the tree it was built from was dirty.
func (i Info) Short() string {
	if i.Modified {
		return i.Version + " (modified)"
	}
	return i.Version
}

// ShortRevision is the abbreviated revision, or empty when unknown.
func (i Info) ShortRevision() string { return shortRevision(i.Revision) }

func shortRevision(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
