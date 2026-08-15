package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

// HandleRegister implements POST /v1/register — invite-only account
// creation (design §8.2). The invite code authenticates the request;
// it's redeemed atomically single-use.
func (s *Server) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Invite   string `json:"invite"`
		Username string `json:"username"`
		Password string `json:"password"`
		Timezone string `json:"timezone,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Invite == "" || req.Username == "" || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "invite, username, and password (>= 8 chars) required")
		return
	}
	ctx := r.Context()
	inv, err := s.St.RedeemInvite(ctx, auth.HashSecret(req.Invite), time.Now())
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid or expired invite")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	id, _ := auth.NewSecret()
	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		tz = "UTC"
	}
	u := store.User{
		ID: id[:16], Name: req.Username, Argon2Hash: hash, Timezone: tz,
		KosyncEnabled: true, KopluginEnabled: true, CreatedAt: time.Now(),
	}
	if err := s.St.CreateUser(ctx, u); err != nil {
		if err == store.ErrConflict {
			writeError(w, http.StatusConflict, "username taken")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	_ = inv
	writeJSON(w, http.StatusCreated, map[string]string{"user_id": u.ID})
}
