package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/liseur-sync/internal/api"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/webui"
)

func TestAnonymousPasswordRoutesShareAdmissionLimit(t *testing.T) {
	for range 2 {
		release := auth.BeginPasswordRequest()
		if release == nil {
			t.Fatal("could not reserve password capacity")
		}
		defer release()
	}
	cfg := config.Default()
	cfg.InsecureHTTP = true
	limiter := auth.NewRateLimiter(100, time.Minute)
	// No store: rejected requests must not reach credential lookup,
	// invite redemption, or first-admin creation.
	server := &api.Server{
		Cfg: cfg, LoginLimiter: limiter,
		WebUI: &webui.Server{Cfg: cfg, LoginLimiter: limiter},
	}
	handler := server.Handler()
	for _, path := range []string{"/v1/login", "/v1/register", "/ui/login", "/ui/setup"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			body := `{"username":"new","password":"long-enough-password","invite":"unused"}`
			contentType := "application/json"
			if strings.HasPrefix(path, "/ui/") {
				body = "username=new&password=long-enough-password&folder_name=MyBooks&folder_root=%2Fbooks"
				contentType = "application/x-www-form-urlencoded"
			}
			req := httptest.NewRequest("POST", path, strings.NewReader(body))
			req.Header.Set("Content-Type", contentType)
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
				t.Fatalf("status=%d headers=%v", w.Code, w.Header())
			}
			if strings.HasPrefix(path, "/ui/") {
				if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), "<form") || !strings.Contains(w.Body.String(), "busy") {
					t.Fatalf("expected a retry form, got %s: %s", w.Header().Get("Content-Type"), w.Body.String())
				}
				if path == "/ui/setup" && (!strings.Contains(w.Body.String(), "MyBooks") || !strings.Contains(w.Body.String(), "/books")) {
					t.Fatal("setup retry lost folder fields")
				}
				if strings.Contains(w.Body.String(), "long-enough-password") {
					t.Fatal("retry form exposed password")
				}
			} else if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
				t.Fatal("API busy response must remain JSON")
			}
		})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("password saturation blocked health: %d", w.Code)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/folders", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("password saturation changed bearer gate: %d", w.Code)
	}
}
