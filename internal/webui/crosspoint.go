package webui

import (
	"errors"
	"net/http"
	"time"

	"github.com/chmouel/liseur-sync/internal/adapter/kosync"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func (s *Server) handleCrossPointPlugin(w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !s.checkCSRF(r, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	key := auth.KosyncUserKey(r.PostForm.Get("password"))
	d, err := s.St.KosyncDeviceByKey(r.Context(), auth.HashSecret(key))
	if err != nil || d.UserID != u.ID || d.DeviceSlot != r.PathValue("slot") || d.RevokedAt != nil || !u.KosyncEnabled {
		http.Error(w, "pairing password does not match this active slot", http.StatusForbidden)
		return
	}
	bundle, err := kosync.CrossPointPlugin(s.serverBaseURL(r), d.DeviceSlot, key)
	if err != nil {
		if errors.Is(err, kosync.ErrPluginConfigTooLarge) {
			http.Error(w, "this device name is too long to fit CrossPoint's 2KB plugin config: pair a device with a shorter name", http.StatusUnprocessableEntity)
			return
		}
		http.Error(w, "could not prepare plugin", http.StatusInternalServerError)
		return
	}
	if _, err := s.St.EnableKosyncStatistics(r.Context(), u.ID, d.DeviceSlot, time.Time{}); err != nil {
		http.Error(w, "could not enable statistics", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="liseur-stats.zip"`)
	w.Write(bundle)
}
