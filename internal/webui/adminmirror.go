package webui

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/mirror"
	"github.com/chmouel/liseur-sync/internal/store"
)

func mirrorPasswordPlaceholder(hasKey bool) string {
	if hasKey {
		return "saved; leave blank to keep it"
	}
	return "required"
}

// mirrorHasCredential reports whether a credential for the configured
// protocol is already on disk, so the form can offer to keep it. The
// two protocols store different things, and a KOReader key left over
// from before a switch is not a BookOrbit password.
func mirrorHasCredential(m config.MirrorConfig) bool {
	if m.Protocol == config.ProtocolBookOrbit {
		return m.RemotePassword != ""
	}
	return m.RemoteKey != ""
}

func mirrorCredentialFromEnv(protocol string) bool {
	if protocol == config.ProtocolBookOrbit {
		_, ok := os.LookupEnv("LISEUR_MIRROR_REMOTE_PASSWORD")
		return ok
	}
	_, ok := os.LookupEnv("LISEUR_MIRROR_REMOTE_KEY")
	return ok
}

func applyMirrorCredentialFromEnv(m *config.MirrorConfig) bool {
	if m.Protocol == config.ProtocolBookOrbit {
		v, ok := os.LookupEnv("LISEUR_MIRROR_REMOTE_PASSWORD")
		if ok {
			m.RemoteKey, m.RemotePassword = "", v
		}
		return ok
	}
	v, ok := os.LookupEnv("LISEUR_MIRROR_REMOTE_KEY")
	if ok {
		m.RemoteKey, m.RemotePassword = v, ""
	}
	return ok
}

// mirrorConfig is what the mirror form shows: the file as this process
// last wrote it, or as it read it at startup.
func (s *Server) mirrorConfig() config.MirrorConfig {
	s.mirrorMu.RLock()
	defer s.mirrorMu.RUnlock()
	if s.mirrorSaved != nil {
		return *s.mirrorSaved
	}
	return s.Cfg.Mirror
}

// rememberMirror records what a save put on disk, so the page the
// browser is redirected to shows the settings it just submitted rather
// than the ones this process started with.
func (s *Server) rememberMirror(m config.MirrorConfig) {
	s.mirrorMu.Lock()
	defer s.mirrorMu.Unlock()
	s.mirrorSaved = &m
}

// handleSaveMirror writes the [mirror] table of the config file.
//
// What the password field means depends on the protocol. On the
// KOReader path it is turned into the KOReader key here and the
// password itself is never stored, because that is what KOReader's
// clients send. On the BookOrbit path it is the account password and
// has to be kept as typed: BookOrbit's own API signs in with it, and
// there is no scoped alternative it would accept.
//
// A save with the mirror enabled is tested against the peer first, so
// a credential that cannot work is refused rather than written and
// left to fail quietly at the next restart.
//
// It finishes with Post/Redirect/Get like every other settings
// mutation: rendering the page under /ui/admin/mirror would leave the
// browser two segments below /ui, where every relative link on the page
// resolves into a directory that does not exist.
func (s *Server) handleSaveMirror(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	m := s.mirrorConfig()
	previous := m.Protocol
	m.Enabled = r.FormValue("enabled") == "on"
	m.Protocol = strings.ToLower(strings.TrimSpace(r.FormValue("protocol")))
	m.BaseURL = strings.TrimSpace(r.FormValue("base_url"))
	m.Account = strings.TrimSpace(r.FormValue("account"))
	m.RemoteUser = strings.TrimSpace(r.FormValue("remote_user"))
	m.Name = strings.TrimSpace(r.FormValue("name"))
	m.DeviceID = strings.TrimSpace(r.FormValue("device_id"))
	m.PeerPathPrefix = strings.TrimSpace(r.FormValue("peer_path_prefix"))
	m.LocalPathPrefix = strings.TrimSpace(r.FormValue("local_path_prefix"))
	// A protocol change invalidates whatever credential was saved for
	// the other one: an MD5 of a KOReader password is not a BookOrbit
	// login, and neither is usable as the other. Clearing it turns a
	// switch with no new password into a form that says the password
	// is required, rather than a connection test that fails obscurely.
	if m.Protocol != previous {
		m.RemoteKey, m.RemotePassword = "", ""
	}
	password := r.FormValue("remote_password")
	if password != "" {
		if m.Protocol == config.ProtocolBookOrbit {
			m.RemotePassword = password
		} else {
			sum := md5.Sum([]byte(password))
			m.RemoteKey = hex.EncodeToString(sum[:])
		}
	} else {
		diskMirror, _, err := config.MirrorFromFile(s.ConfigPath)
		if err != nil {
			s.renderMirror(w, r, a, u, Flash{Error: err.Error()})
			return
		}
		if diskMirror.Protocol == m.Protocol {
			// Keep only a credential that was already in the file. A
			// credential supplied through the environment may still be used
			// to test the connection below, but saving unrelated settings
			// must not copy it into the TOML file.
			m.RemoteKey, m.RemotePassword = diskMirror.RemoteKey, diskMirror.RemotePassword
		} else {
			m.RemoteKey, m.RemotePassword = "", ""
		}
	}
	// Validate normalizes as well as checks — it trims and strips a
	// trailing slash from the peer URL — so what it leaves behind is
	// what gets written, not what the form sent.
	checkMirror := m
	if password == "" && mirrorCredentialFromEnv(checkMirror.Protocol) {
		// The running config is the effective one after environment
		// overrides. Use that credential for validation and the
		// connection test, while the value saved below stays the one
		// from the file unless the form supplied a replacement.
		applyMirrorCredentialFromEnv(&checkMirror)
	}
	check := s.Cfg
	check.Mirror = checkMirror
	if err := check.Validate(); err != nil {
		s.renderMirror(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	checkMirror = check.Mirror
	// Keep the normalized non-secret fields from validation, but keep
	// the credential decision from above: it may intentionally be blank
	// on disk while present in the process environment.
	m.Protocol = checkMirror.Protocol
	m.Name = checkMirror.Name
	m.BaseURL = checkMirror.BaseURL
	m.Account = checkMirror.Account
	m.RemoteUser = checkMirror.RemoteUser
	m.DeviceID = checkMirror.DeviceID
	m.PeerPathPrefix = checkMirror.PeerPathPrefix
	m.LocalPathPrefix = checkMirror.LocalPathPrefix
	m.PollInterval = checkMirror.PollInterval
	m.ResolveRetryInterval = checkMirror.ResolveRetryInterval
	m.ActiveDays = checkMirror.ActiveDays
	m.Timeout = checkMirror.Timeout
	if checkMirror.Enabled {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		proto, err := mirror.NewProtocol(checkMirror, nil)
		if err != nil {
			s.renderMirror(w, r, a, u, Flash{Error: err.Error()})
			return
		}
		// The test signs in, so it leaves a session behind on a peer
		// that keeps them. Closing it is the difference between
		// editing this page and accumulating logins on the peer.
		defer proto.Close(ctx)
		if err := proto.Authorize(ctx); err != nil {
			s.renderMirror(w, r, a, u, Flash{Error: "The peer rejected the connection: " + err.Error()})
			return
		}
	}
	if err := config.SaveMirror(s.ConfigPath, m); err != nil {
		s.renderMirror(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	s.rememberMirror(m)
	notice := "Mirror saved and disabled. Restart liseur-sync to stop it."
	if m.Enabled {
		notice = "Mirror saved and connection tested. Restart liseur-sync to apply it."
	}
	s.renderMirror(w, r, a, u, Flash{Notice: notice})
}

// renderMirror lands the browser back on the mirror page, carrying the
// flash in the query string.
func (s *Server) renderMirror(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	if settingsRedirect(w, r, settingsAdmin, settingsAdminMirror, "", flash) {
		return
	}
	s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminMirror, "", flash, false, "", false)
}
