// Package kosync implements the /adapter/kosync/* endpoints: the four
// calls stock KOReader's kosync plugin makes. It translates the legacy
// wire format into native store records (design §7.1).
//
// Auth model: kosync's "username/password" is a dedicated pairing
// credential, never the account password. The user generates a pairing
// code (admin CLI / web UI) and enters it as the kosync password;
// users/create redeems the code and binds a device slot whose key
// (KOReader's MD5-derived key) is stored hashed. Every adapter request
// authenticates with x-auth-user / x-auth-key headers.
package kosync

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// Server is the kosync adapter.
type Server struct {
	St          store.Store
	OpenReg     bool // open_registration config
	PairingTTL  time.Duration
	AuthRateLim *auth.RateLimiter
	PairingUser func(r *http.Request) string // reserved for future web pairing
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// md5hex mirrors KOReader's derivation: the kosync "password" entered
// by the user is MD5'd to become the auth key.
func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// deviceFrom authenticates x-auth-user/x-auth-key. The key is
// MD5(pairing code) as derived by KOReader, stored hashed (SHA-256);
// it globally identifies the device slot. The username must match the
// slot name (constant-time).
func (s *Server) deviceFrom(r *http.Request) (store.KosyncDevice, error) {
	user := r.Header.Get("x-auth-user")
	key := r.Header.Get("x-auth-key")
	if user == "" || key == "" {
		return store.KosyncDevice{}, errAuth
	}
	d, err := s.St.KosyncDeviceByKey(r.Context(), auth.HashSecret(key))
	if err != nil || d.RevokedAt != nil {
		return store.KosyncDevice{}, errAuth
	}
	if subtle.ConstantTimeCompare([]byte(d.DeviceSlot), []byte(user)) != 1 {
		return store.KosyncDevice{}, errAuth
	}
	// Per-user adapter gate.
	u, err := s.St.UserByID(r.Context(), d.UserID)
	if err != nil || !u.KosyncEnabled {
		return store.KosyncDevice{}, errAuth
	}
	return d, nil
}

var errAuth = errString("invalid kosync credentials")

type errString string

func (e errString) Error() string { return string(e) }

// HandleAuth implements GET /users/auth.
func (s *Server) HandleAuth(w http.ResponseWriter, r *http.Request) {
	if _, err := s.deviceFrom(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"authorized":"OK"}`))
}

// HandleCreateUser implements POST /users/create. With open
// registration off (default), the kosync "password" must be a valid
// pairing code; the code is redeemed and a device slot created, keyed
// on MD5(pairing code) — the key KOReader will present on future calls.
func (s *Server) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username and password required"})
		return
	}

	ctx := r.Context()
	now := time.Now()

	if s.OpenReg {
		// Open registration: the password IS the account password; create
		// the account and a device slot. Off by default (design §7.1).
		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		id, _ := auth.NewSecret()
		u := store.User{
			ID: id[:16], Name: req.Username, Argon2Hash: hash, Timezone: "UTC",
			KosyncEnabled: true, KopluginEnabled: true, CreatedAt: now,
		}
		if err := s.St.CreateUser(ctx, u); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "username taken"})
			return
		}
		d := store.KosyncDevice{
			UserID: u.ID, DeviceSlot: req.Username,
			KeySHA256: auth.HashSecret(md5hex(req.Password)), Label: "kosync",
		}
		if err := s.St.CreateKosyncDevice(ctx, d); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username})
		return
	}

	// Pairing flow: password is a pairing code. KOReader will derive
	// MD5(code) as its auth key, so the slot key is the hash of that.
	p, err := s.St.RedeemPairingCode(ctx, auth.HashSecret(req.Password), now)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or expired pairing code"})
		return
	}
	d := store.KosyncDevice{
		UserID:     p.UserID,
		DeviceSlot: req.Username, // kosync "username" names the device slot
		KeySHA256:  auth.HashSecret(md5hex(req.Password)),
		Label:      "kosync:" + req.Username,
	}
	if err := s.St.CreateKosyncDevice(ctx, d); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "device slot already paired"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username})
}

// progressRequest is kosync's PUT body.
type progressRequest struct {
	Document   string  `json:"document"`
	Progress   string  `json:"progress"`   // CRe xpointer
	Percentage float64 `json:"percentage"` // 0..1
	Device     string  `json:"device"`
	DeviceID   string  `json:"device_id"`
	Timestamp  int64   `json:"timestamp"`
}

// HandlePutProgress implements PUT /syncs/progress. Translates to a
// native op: origin=kosync, foreign_pos verbatim, progression as sent
// (0 accepted — the falsy-zero bug is a named regression test).
// Unresolvable digests create a pending work so history is not lost.
func (s *Server) HandlePutProgress(w http.ResponseWriter, r *http.Request) {
	d, err := s.deviceFrom(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req progressRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Document == "" || req.Progress == "" {
		writeError := map[string]string{"error": "document and progress required"}
		writeJSON(w, http.StatusBadRequest, writeError)
		return
	}
	if req.Percentage < 0 || req.Percentage > 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "percentage out of range"})
		return
	}
	ctx := r.Context()

	clientTS := time.Now()
	if req.Timestamp > 0 {
		clientTS = time.Unix(req.Timestamp, 0) // kosync sends unix seconds
	}
	opID, _ := auth.NewSecret()
	op := store.Op{
		OpID:        opID,
		DeviceID:    "kosync:" + d.DeviceSlot,
		ClientTS:    clientTS,
		Progression: req.Percentage,
		ForeignPos:  &req.Progress,
		Origin:      store.OriginKosync,
		OriginAlias: strPtr("partial-md5:" + strings.ToLower(req.Document)),
	}
	res, err := s.St.AppendKosyncOp(
		ctx, d.UserID, strings.ToLower(req.Document), "kosync:"+d.DeviceSlot, op)
	if err != nil || res.Status == "conflict" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"document":  req.Document,
		"timestamp": time.Now().Unix(),
	})
}

// HandleGetProgress implements GET /syncs/progress/:document. Returns
// the newest op, kosync-shaped: progress = foreign_pos verbatim when
// present, else the percentage as a bare string (KOReader accepts that
// for non-CRe engines).
func (s *Server) HandleGetProgress(w http.ResponseWriter, r *http.Request) {
	d, err := s.deviceFrom(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	doc := r.PathValue("document")
	workID, err := s.St.WorkIDByAlias(r.Context(), d.UserID, "partial-md5", strings.ToLower(doc))
	if err != nil {
		// kosync returns 200 with an empty body-ish when unknown; the
		// real server returns {"error":"not found"} — mirror that.
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	ops, err := s.St.Positions(r.Context(), d.UserID, workID, 1)
	if err != nil || len(ops) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	o := ops[0]
	progress := ""
	if o.ForeignPos != nil {
		progress = *o.ForeignPos
	} else {
		progress = json.Number(floatToStr(o.Progression)).String()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"document":   doc,
		"progress":   progress,
		"percentage": o.Progression,
		"device":     o.DeviceID,
		"timestamp":  o.ClientTS.Unix(),
	})
}

func floatToStr(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func strPtr(s string) *string { return &s }

// Mount registers the kosync adapter routes. InsecureHTTP and trusted
// proxies are enforced by the caller's middleware chain.
func (s *Server) Mount(mux *http.ServeMux, secure func(http.Handler) http.Handler) {
	mux.Handle("POST /adapter/kosync/users/create",
		secure(auth.RateLimitIP(s.AuthRateLim, http.HandlerFunc(s.HandleCreateUser))))
	mux.Handle("GET /adapter/kosync/users/auth",
		secure(auth.RateLimitIP(s.AuthRateLim, http.HandlerFunc(s.HandleAuth))))
	mux.Handle("PUT /adapter/kosync/syncs/progress",
		secure(http.HandlerFunc(s.HandlePutProgress)))
	mux.Handle("GET /adapter/kosync/syncs/progress/{document}",
		secure(http.HandlerFunc(s.HandleGetProgress)))
}
