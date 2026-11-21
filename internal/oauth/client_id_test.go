package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

type clientLookupStore struct {
	store.Store
	lookups int
}

func (s *clientLookupStore) OAuthClient(context.Context, string) (store.OAuthClient, error) {
	s.lookups++
	return store.OAuthClient{}, store.ErrNotFound
}

func TestMalformedClientIDsNeverReachStore(t *testing.T) {
	st := &clientLookupStore{}
	s := New(st, config.Default())
	for name, id := range map[string]string{
		"empty": "", "too long": strings.Repeat("a", 513),
		"NUL": "client\x00id", "invalid UTF-8": "client\xffid",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.ResolveClient(t.Context(), id); !errors.Is(err, ErrUnknownClient) {
				t.Fatalf("authorize: %v", err)
			}
			if _, err := s.ParseAuthorize(t.Context(), url.Values{"client_id": {id}}); err == nil {
				t.Fatal("accepted malformed authorization client")
			} else if ae, ok := err.(*AuthorizeError); !ok || ae.Code != "unauthorized_client" || ae.Redirect != "" || ae.Description != ErrUnknownClient.Error() {
				t.Fatalf("authorization error: %v", err)
			}
			for _, basic := range []bool{false, true} {
				form := url.Values{"grant_type": {"authorization_code"}}
				if !basic {
					form.Set("client_id", id)
				}
				r := httptest.NewRequest(http.MethodPost, TokenPath, strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if basic {
					r.SetBasicAuth(id, "secret")
				}
				w := httptest.NewRecorder()
				s.HandleToken(w, r)
				var body tokenError
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusUnauthorized || body.Error != "invalid_client" || w.Header().Get("WWW-Authenticate") == "" {
					t.Fatalf("basic %t: %d %s", basic, w.Code, w.Body.String())
				}
			}
			if st.lookups != 0 {
				t.Fatalf("malformed client reached store: %d lookups", st.lookups)
			}
		})
	}
	if _, err := s.ResolveClient(t.Context(), strings.Repeat("a", 512)); !errors.Is(err, ErrUnknownClient) || st.lookups != 1 {
		t.Fatalf("512-byte client ID: %v, lookups %d", err, st.lookups)
	}
}
