//go:build linux

package api

import (
	"net/http"
	"testing"

	"github.com/chmouel/liseur-sync/internal/store"
)

// TestMeAnswersOnlyForTheTokensOwnAccount: /v1/me exists so the browser
// reader served from the detached origin — which has no session and no
// user — can find out what prompt the account keeps. It reads the
// credential's own account and nothing else, and it is closed to a
// caller with no credential at all.
func TestMeAnswersOnlyForTheTokensOwnAccount(t *testing.T) {
	f := newFolderFixture(t)
	const prompt = `Reading {title} at {percent}%: "{text}"`
	if err := f.st.UpdateUserSettings(t.Context(), f.user.ID, store.UserSettings{
		Timezone: "Europe/Paris", ReaderPromptTemplate: prompt,
	}); err != nil {
		t.Fatal(err)
	}

	code, me := getJSON(t, f.ts.URL+"/v1/me", f.token)
	if code != http.StatusOK {
		t.Fatalf("me: %d %v", code, me)
	}
	if me["id"] != f.user.ID || me["name"] != f.user.Name {
		t.Fatalf("me described another account: %v", me)
	}
	if me["timezone"] != "Europe/Paris" || me["reader_prompt_template"] != prompt {
		t.Fatalf("me = %v", me)
	}

	// Another account's token sees that account, never this one's.
	other := f.mintToken(t, f.other.ID, store.ScopeLibraryRead)
	code, theirs := getJSON(t, f.ts.URL+"/v1/me", other)
	if code != http.StatusOK {
		t.Fatalf("the other account's me: %d %v", code, theirs)
	}
	if theirs["id"] != f.other.ID || theirs["reader_prompt_template"] != "" {
		t.Fatalf("one account read another's prompt: %v", theirs)
	}

	// An account that has never written one has none, and a request
	// with no credential is refused like every other route.
	if code, _ = getJSON(t, f.ts.URL+"/v1/me", ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated me: %d, want 401", code)
	}

	// A credential that cannot read the library cannot read the
	// account behind it either.
	syncOnly := f.mintToken(t, f.user.ID, store.ScopeSync)
	if code, _ = getJSON(t, f.ts.URL+"/v1/me", syncOnly); code != http.StatusForbidden {
		t.Fatalf("a sync-only token reached /v1/me: %d", code)
	}
}
