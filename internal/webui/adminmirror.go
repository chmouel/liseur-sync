package webui

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/mirror"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The mirror page edits one file section and lists the rest. It reads
// the file on every request rather than the configuration this process
// started with, so a mirror saved and left disabled is still there to
// edit, while the running worker keeps the settings it booted with until
// the server is restarted (ADR-0061).
const (
	mirrorEditQuery = "mirror"
	mirrorAddQuery  = "add"
)

func mirrorEditHref(prefix, name string) string {
	return settingsAdminHref(prefix, settingsAdminMirror) +
		"&" + mirrorEditQuery + "=" + url.QueryEscape(name)
}

func mirrorAddHref(prefix string) string {
	return settingsAdminHref(prefix, settingsAdminMirror) + "&" + mirrorAddQuery + "=1"
}

func mirrorProtocolLabel(protocol string) string {
	if protocol == config.ProtocolBookOrbit {
		return "BookOrbit API"
	}
	return "KOReader sync"
}

// mirrorCleanURL is the address as the page shows it: a scheme, a host
// and a path, never with the credential somebody pasted into it. The
// validator refuses those, so this is only for showing a URL it has not
// seen yet; a value it could not read shows as nothing rather than as
// text nobody has looked at.
func mirrorCleanURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

// mirrorRootKey is an address normalised for comparison only: the file may
// spell one root with whitespace or an upper-case scheme, which validation
// accepts while storing its own spelling. Two entries are the same root
// when their keys are equal, and a value that does not read as an address
// has no key to compare.
func mirrorRootKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return config.MirrorRoot(u.String())
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

func mirrorPasswordPlaceholder(hasKey bool) string {
	if hasKey {
		return "saved; leave blank to keep it"
	}
	return "required"
}

// mirrorCredentialEnvName is the one environment variable the entry's
// protocol reads its credential from.
func mirrorCredentialEnvName(m config.MirrorConfig, legacy bool) string {
	keyVar, passwordVar := config.MirrorCredentialEnv(m.Name, legacy)
	if m.Protocol == config.ProtocolBookOrbit {
		return passwordVar
	}
	return keyVar
}

// mirrorCredentialSource says where the credential comes from, which is
// the one thing an editor must not have to guess: the file is written by
// this page, an environment variable is not, and an entry that has
// neither will not start.
func mirrorCredentialSource(m config.MirrorConfig, legacy bool) string {
	envName := mirrorCredentialEnvName(m, legacy)
	if value, ok := os.LookupEnv(envName); ok {
		if value == "" {
			return envName + " (set but empty: this mirror will not start)"
		}
		return "environment: " + envName
	}
	if mirrorHasCredential(m) {
		return "the configuration file"
	}
	return "missing"
}

// mirrorView builds the page: every entry the file holds, and the form
// for the selected one.
func (s *Server) mirrorView(r *http.Request, users []store.User) (mirrorView, error) {
	prefix := relPrefix(r.URL.Path)
	v := mirrorView{Users: users, CanAdd: true, AddHref: mirrorAddHref(prefix)}
	file, err := config.ReadMirrors(s.ConfigPath)
	if err != nil {
		// A file that does not parse is still repairable through the
		// single entry, and only through it: the running configuration
		// is what the page can offer until it does.
		v.FileError = err.Error()
		file = config.MirrorFile{
			Legacy: s.Cfg.Mirror, HasLegacy: s.Cfg.MirrorTableDefined(),
		}
		v.CanAdd = false
	}
	// What the page lists is the set the next start would run, not the
	// file's half of it: an entry the environment supplies has to be
	// visible (read-only, since the file cannot change it), or an
	// administrator would add a list entry whose name it already takes and
	// only find out on the next start.
	effective := file.WithEnvironment()
	if effective.HasLegacy {
		v.Entries = append(v.Entries, s.mirrorEntry(effective.Legacy, true, prefix))
	}
	for _, m := range effective.List {
		v.Entries = append(v.Entries, s.mirrorEntry(m, false, prefix))
	}

	switch {
	case r.URL.Query().Get(mirrorAddQuery) == "1" && v.CanAdd:
		blank := config.Default().Mirror
		// The default name is the single entry's, not a suggestion for
		// a new one: a list entry called "peer" would collide with the
		// table as soon as a deployment has one.
		blank.Name = ""
		v.Edit = mirrorEditViewFor(blank, false, "")
	case r.URL.Query().Get(mirrorEditQuery) != "":
		name := r.URL.Query().Get(mirrorEditQuery)
		if m, ok := file.Entry(name); ok {
			v.Edit = mirrorEditViewFor(m, false, name)
		} else if entry, ok := file.LegacyEntry(); ok && entry.Name == name {
			v.Edit = mirrorEditViewFor(entry, true, name)
		}
	}
	return v, nil
}

func (s *Server) mirrorEntry(m config.MirrorConfig, legacy bool, prefix string) mirrorEntryView {
	e := mirrorEntryView{
		Name:          m.Name,
		ProtocolLabel: mirrorProtocolLabel(m.Protocol),
		URL:           mirrorCleanURL(m.BaseURL),
		Account:       m.Account,
		Credential:    mirrorCredentialSource(m, legacy),
		Enabled:       m.Enabled,
		Legacy:        legacy,
		EditHref:      mirrorEditHref(prefix, m.Name),
	}
	if legacy && config.LegacyMirrorEnvironmentManaged() {
		// Every non-secret setting of this entry comes from the
		// environment at startup, so a form that saved others would be
		// saving values the next start throws away.
		e.ReadOnly = true
		e.EditHref = ""
	}
	return e
}

// mirrorEditViewFor prepares the form for one entry, clearing the
// credential on the way: a page never carries a secret, and the form has
// no field that could show one back.
func mirrorEditViewFor(m config.MirrorConfig, legacy bool, original string) *mirrorEditView {
	// The credential variable depends on the protocol and the name, and
	// both come from a file an operator wrote: this is the same
	// normalisation validation applies, done before the lookup.
	m.Name = strings.TrimSpace(m.Name)
	m.Protocol = strings.ToLower(strings.TrimSpace(m.Protocol))
	// A URL the file holds but validation has not accepted yet may carry
	// a credential, and the form has no field that could show one back.
	m.BaseURL = mirrorCleanURL(m.BaseURL)
	v := &mirrorEditView{
		Config:            m,
		Original:          original,
		Legacy:            legacy,
		BookOrbit:         m.Protocol == config.ProtocolBookOrbit,
		HasKey:            mirrorHasCredential(m),
		CredentialFromEnv: mirrorCredentialEnvName(m, legacy),
		Remove:            original != "",
	}
	if _, ok := os.LookupEnv(v.CredentialFromEnv); !ok {
		v.CredentialFromEnv = ""
	}
	v.Config.RemoteKey, v.Config.RemotePassword = "", ""
	return v
}

// mirrorFromForm is the entry the form describes. A field the form does
// not carry keeps what the entry on disk had, which is what makes this an
// edit rather than a replacement.
func mirrorFromForm(r *http.Request, previous config.MirrorConfig, has bool) config.MirrorConfig {
	m := previous
	if !has {
		m = config.Default().Mirror
	}
	m.Enabled = r.FormValue("enabled") == "on"
	m.Protocol = strings.ToLower(strings.TrimSpace(r.FormValue("protocol")))
	if has {
		// The name keys this mirror's bookkeeping and prefixes the
		// device a position from it is filed under, so it is not
		// editable: a rename is a removal and a fresh entry.
		m.Name = previous.Name
	} else {
		m.Name = strings.TrimSpace(r.FormValue("name"))
	}
	m.BaseURL = strings.TrimSpace(r.FormValue("base_url"))
	m.Account = strings.TrimSpace(r.FormValue("account"))
	m.RemoteUser = strings.TrimSpace(r.FormValue("remote_user"))
	m.DeviceID = strings.TrimSpace(r.FormValue("device_id"))
	m.PeerPathPrefix = strings.TrimSpace(r.FormValue("peer_path_prefix"))
	m.LocalPathPrefix = strings.TrimSpace(r.FormValue("local_path_prefix"))
	// A protocol change invalidates whatever credential was saved for the
	// other one: an MD5 of a KOReader password is not a BookOrbit login,
	// and neither is usable as the other. Clearing it turns a switch with
	// no new password into a form that says the password is required,
	// rather than a connection test that fails obscurely.
	if m.Protocol != previous.Protocol {
		m.RemoteKey, m.RemotePassword = "", ""
	}
	// What the password field means depends on the protocol. On the
	// KOReader path it is turned into the KOReader key here and the
	// password itself is never stored, because that is what KOReader's
	// clients send. On the BookOrbit path it is the account password and
	// has to be kept as typed: BookOrbit's own API signs in with it, and
	// there is no scoped alternative it would accept.
	if password := r.FormValue("remote_password"); password != "" {
		if m.Protocol == config.ProtocolBookOrbit {
			m.RemoteKey, m.RemotePassword = "", password
		} else {
			sum := md5.Sum([]byte(password))
			m.RemoteKey, m.RemotePassword = hex.EncodeToString(sum[:]), ""
		}
	}
	return m
}

// mirrorCandidate validates a whole mirror set the way startup would: the
// file's entries with the same environment overlay a start applies, so an
// entry the environment supplies is counted even when the file has no table,
// and a credential the environment owns is the one a test uses. The file's own
// values are what get written.
func (s *Server) mirrorCandidate(f config.MirrorFile) (config.Config, error) {
	c := s.Cfg
	c.Mirror = f.Legacy
	c.Mirrors = append([]config.MirrorConfig(nil), f.List...)
	c.SetMirrorTableDefined(f.HasLegacy)
	c.ApplyMirrorEnvironment()
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// adoptMirrorSettings takes back from validation the settings it
// normalises — a trimmed URL, a lower-case protocol, the defaults an
// omitted field means — while leaving each entry's credential as the file
// will hold it and each entry's address spelled as the entry spelled it.
//
// The spelling is not decoration: validation records it to recognise
// cursors an earlier build folded with the slashes still on, so cleaning
// an address nobody edited would strand that cursor. Validation accepts
// spellings of its own (the whitespace around an address, an upper-case
// scheme), so what is compared is the root the two spellings name.
func adoptMirrorSettings(f *config.MirrorFile, c config.Config) {
	adopt := func(dst *config.MirrorConfig, src config.MirrorConfig) {
		key, password, baseURL := dst.RemoteKey, dst.RemotePassword, dst.BaseURL
		*dst = src
		dst.RemoteKey, dst.RemotePassword = key, password
		// Only the entry's own spelling is kept back, and only when it
		// names the root validation accepted: an address an
		// administrator edited is the one that gets validated and
		// written.
		if root := mirrorRootKey(baseURL); root != "" && root == mirrorRootKey(dst.BaseURL) {
			dst.BaseURL = baseURL
		}
	}
	if f.HasLegacy && !config.LegacyMirrorEnvironmentManaged() {
		// An entry the environment owns is shown read-only and refused
		// for editing, but a list save still runs through here: copying
		// the overlaid settings into the file would persist values the
		// operator keeps in the environment.
		adopt(&f.Legacy, c.Mirror)
	}
	// Validation normalises entries where they are, so the two lists
	// are the same length and the same order.
	if len(f.List) == len(c.Mirrors) {
		for i := range f.List {
			adopt(&f.List[i], c.Mirrors[i])
		}
	}
}

// candidateEntry finds an entry in the validated set, so a connection
// test uses the credential that would actually be used.
func candidateEntry(c config.Config, name string, legacy bool) (config.MirrorConfig, bool) {
	if legacy && c.Mirror.Name == name {
		return c.Mirror, true
	}
	for _, m := range c.Mirrors {
		if m.Name == name {
			return m, true
		}
	}
	return config.MirrorConfig{}, false
}

// saveMirror writes one entry, tests it when it ends up enabled, and
// returns what to tell the administrator.
//
// The connection test costs a request to somebody else's server, so it
// runs only for the entry being changed, and only when it is enabled:
// saving an unrelated setting must not depend on a peer that is down.
func (s *Server) saveMirror(r *http.Request, original string, legacy bool) (string, error) {
	var tested bool
	err := config.UpdateMirrors(s.ConfigPath, func(f *config.MirrorFile) error {
		previous, has, index := mirrorEntryToReplace(*f, original, legacy)
		if !legacy && original != "" && !has {
			return fmt.Errorf("there is no mirror named %q", original)
		}
		m := mirrorFromForm(r, previous, has)
		switch {
		case legacy:
			f.Legacy, f.HasLegacy = m, true
		case has:
			f.List[index] = m
		default:
			if _, exists := f.Entry(m.Name); exists {
				return fmt.Errorf("a mirror named %q already exists", m.Name)
			}
			f.List = append(f.List, m)
		}
		candidate, err := s.mirrorCandidate(*f)
		if err != nil {
			return err
		}
		adoptMirrorSettings(f, candidate)
		entry, ok := candidateEntry(candidate, m.Name, legacy)
		if !ok || !entry.Enabled {
			return nil
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		proto, err := mirror.NewProtocol(entry, nil)
		if err != nil {
			return err
		}
		// The test signs in, so it leaves a session behind on a peer
		// that keeps them. Closing it is the difference between editing
		// this page and accumulating logins on the peer.
		defer proto.Close(ctx)
		if err := proto.Authorize(ctx); err != nil {
			return fmt.Errorf("the peer rejected the connection: %w", err)
		}
		tested = true
		return nil
	})
	if err != nil {
		return "", err
	}
	if tested {
		return "Mirror saved and connection tested. Restart liseur-sync to apply it.", nil
	}
	return "Mirror saved and disabled. Restart liseur-sync to stop or apply it.", nil
}

// mirrorEntryToReplace finds the entry an edit is about: the single
// [mirror] table, or a list entry by name. The index is meaningless for
// the table.
func mirrorEntryToReplace(f config.MirrorFile, original string, legacy bool) (config.MirrorConfig, bool, int) {
	if legacy {
		return f.Legacy, f.HasLegacy, -1
	}
	for i, m := range f.List {
		if m.Name == original {
			return m, true, i
		}
	}
	return config.MirrorConfig{}, false, -1
}

// removeMirror takes one entry out of the file. Its cursors stay behind:
// they are keyed by the name and stamped with the peer, so adding the same
// name and peer back resumes them, and a name that never comes back costs
// a few rows nothing reads.
func (s *Server) removeMirror(name string, legacy bool) error {
	return config.UpdateMirrors(s.ConfigPath, func(f *config.MirrorFile) error {
		if legacy {
			if !f.HasLegacy {
				return errors.New("there is no single [mirror] entry")
			}
			f.Legacy, f.HasLegacy = config.MirrorConfig{}, false
			return nil
		}
		list := make([]config.MirrorConfig, 0, len(f.List))
		found := false
		for _, m := range f.List {
			if m.Name == name {
				found = true
				continue
			}
			list = append(list, m)
		}
		if !found {
			return fmt.Errorf("there is no mirror named %q", name)
		}
		f.List = list
		return nil
	})
}

// errForgedCSRF is the outcome an audit line records for a mirror mutation
// whose form token did not match. The token itself is never logged.
var errForgedCSRF = errors.New("the form's CSRF token was refused")

// handleSaveMirror creates or updates one mirror entry.
//
// It is the same gate as every other cross-user mutation: the per-session
// CSRF token, the acting administrator's own password against both reauth
// budgets, one audit line, and a connection test before anything is
// written. What it never does is write a credential the environment owns,
// or render one.
func (s *Server) handleSaveMirror(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		logAdminAction(r, u, "mirror.save",
			mirrorTarget(r.FormValue("original"), r.FormValue("name")), errForgedCSRF)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	original := strings.TrimSpace(r.FormValue("original"))
	legacy := r.FormValue("legacy") == "1"
	if legacy && config.LegacyMirrorEnvironmentManaged() {
		err := errors.New("this mirror's settings come from the environment; " +
			"change the LISEUR_MIRROR_* variables and restart")
		logAdminAction(r, u, "mirror.save", original, err)
		s.renderMirror(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	if err := s.reauth(r, u); err != nil {
		logAdminAction(r, u, "mirror.save", mirrorTarget(original, r.FormValue("name")), err)
		s.mirrorRefusal(w, r, a, u, err)
		return
	}
	notice, err := s.saveMirror(r, original, legacy)
	logAdminAction(r, u, "mirror.save", mirrorTarget(original, r.FormValue("name")), err)
	if err != nil {
		s.renderMirror(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	s.renderMirror(w, r, a, u, Flash{Notice: notice})
}

// handleDeleteMirror removes one entry.
func (s *Server) handleDeleteMirror(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	if !s.checkCSRF(r, a) {
		logAdminAction(r, u, "mirror.delete",
			strings.TrimSpace(r.FormValue("name")), errForgedCSRF)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	legacy := r.FormValue("legacy") == "1"
	if legacy && config.LegacyMirrorEnvironmentManaged() {
		err := errors.New("this mirror's settings come from the environment; " +
			"unset the LISEUR_MIRROR_* variables and restart")
		logAdminAction(r, u, "mirror.delete", name, err)
		s.renderMirror(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	if err := s.reauth(r, u); err != nil {
		logAdminAction(r, u, "mirror.delete", name, err)
		s.mirrorRefusal(w, r, a, u, err)
		return
	}
	err := s.removeMirror(name, legacy)
	logAdminAction(r, u, "mirror.delete", name, err)
	if err != nil {
		s.renderMirror(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	s.renderMirror(w, r, a, u, Flash{Notice: "Mirror removed. Restart liseur-sync to stop it. " +
		"Its bookkeeping stays behind and resumes if the same name and peer come back."})
}

// mirrorTarget is the entry an audit line is about. The name alone: an
// audit trail says what changed, never what it was changed to.
func mirrorTarget(original, submitted string) string {
	if original != "" {
		return original
	}
	return strings.TrimSpace(submitted)
}

// mirrorRefusal answers a failed reauth, in place rather than by
// redirect: a redirect would erase the status the browser has to see,
// which is the 429 the reauth budget answers with.
func (s *Server) mirrorRefusal(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, err error,
) {
	if errors.Is(err, errRateLimited) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminMirror, "",
			Flash{Error: err.Error()}, false, "", false)
		return
	}
	s.renderMirror(w, r, a, u, Flash{Error: err.Error()})
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
