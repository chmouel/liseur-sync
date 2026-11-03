package kosync

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/store"
)

func TestCrossPointSession(t *testing.T) {
	ts, st := testServer(t)
	hash, _ := auth.HashPassword("hunter2hunter")
	u := store.User{ID: "sessions", Name: "alice", Argon2Hash: hash, KosyncEnabled: true, CreatedAt: time.Now()}
	if err := st.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	user, key, _ := pairDevice(t, ts, st, u.ID, "x3")
	now := time.Now().UTC().Truncate(time.Second)
	event := sessionEvent{EventID: "event-1", Document: "0123456789abcdef0123456789abcdef", Start: fmt.Sprint(now.Add(-time.Hour).Unix()), End: fmt.Sprint(now.Add(-time.Hour + time.Minute).Unix()), Emitted: fmt.Sprint(now.Unix()), Duration: "60", StartProgress: "0", EndProgress: "100", Scale: "10000"}
	endpoint := ts.URL + "/adapter/kosync/sessions"
	check := func(e sessionEvent, k string, want int, status string) {
		t.Helper()
		code, out := kreq(t, "POST", endpoint, user, k, e)
		if code != want || (status != "" && out["status"] != status) {
			t.Fatalf("session response: %d %v; want %d %s", code, out, want, status)
		}
	}
	check(event, "bad", http.StatusUnauthorized, "")
	check(event, key, http.StatusConflict, "")
	if _, err := st.EnableKosyncStatistics(t.Context(), u.ID, user, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	check(event, key, http.StatusOK, "stored")
	check(event, key, http.StatusOK, "stored")
	sessions, err := st.SessionsInRange(t.Context(), u.ID, now.Add(-2*time.Hour), now.Add(time.Hour))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %+v %v", sessions, err)
	}
	got := sessions[0]
	if !got.StartedAt.Equal(now.Add(-time.Hour)) || !got.EndedAt.Equal(now) || got.ActiveMs == nil || *got.ActiveMs != 60000 || got.StartProg != 0 || got.EndProg != .01 || got.Origin != store.OriginKosync || got.DeviceID != "kosync:x3" || got.EditionSHA != nil || got.OriginAlias == nil || *got.OriginAlias != "partial-md5:"+event.Document {
		t.Fatalf("measured session: %+v", got)
	}
	work, err := st.WorkByID(t.Context(), u.ID, got.WorkID)
	if err != nil || !work.Pending {
		t.Fatalf("unknown document work: %+v %v", work, err)
	}
	changed := event
	changed.EndProgress = "101"
	check(changed, key, http.StatusConflict, "")
	for name, change := range map[string]func(*sessionEvent){
		"missing start":              func(e *sessionEvent) { e.Start = "" },
		"missing progress":           func(e *sessionEvent) { e.StartProgress = "" },
		"backwards progress":         func(e *sessionEvent) { e.StartProgress = "101" },
		"wrong scale":                func(e *sessionEvent) { e.Scale = "100" },
		"bad document":               func(e *sessionEvent) { e.Document = "book.epub" },
		"compact end":                func(e *sessionEvent) { e.End = e.Emitted },
		"duration exceeds wall time": func(e *sessionEvent) { e.Duration = "3601" },
		"untrusted start":            func(e *sessionEvent) { e.Start = "0" },
		"future emission":            func(e *sessionEvent) { e.Emitted = fmt.Sprint(now.Add(time.Hour).Unix()) },
	} {
		t.Run(name, func(t *testing.T) { bad := event; change(&bad); check(bad, key, http.StatusBadRequest, "") })
	}
	old := event
	old.EventID = "before-cutoff"
	old.Start = fmt.Sprint(now.Add(-3 * time.Hour).Unix())
	old.End = fmt.Sprint(now.Add(-3*time.Hour + time.Minute).Unix())
	check(old, key, http.StatusOK, "ignored")
	if err := st.UpdateUserSettings(t.Context(), u.ID, store.UserSettings{Timezone: "UTC", KosyncEnabled: false}); err != nil {
		t.Fatal(err)
	}
	check(event, key, http.StatusUnauthorized, "")
	if err := st.UpdateUserSettings(t.Context(), u.ID, store.UserSettings{Timezone: "UTC", KosyncEnabled: true}); err != nil {
		t.Fatal(err)
	}

	if err := st.RevokeKosyncDevice(t.Context(), u.ID, user); err != nil {
		t.Fatal(err)
	}
	check(event, key, http.StatusUnauthorized, "")
}
