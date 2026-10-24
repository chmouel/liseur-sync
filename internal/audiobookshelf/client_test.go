package audiobookshelf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chmouel/liseur-sync/internal/config"
)

const testKey = "s3cr3t-key"

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(config.AudiobookshelfConfig{
		BaseURL: srv.URL + "/", APIKey: testKey, Timeout: config.Duration(5 * time.Second),
	})
}

func TestClientSendsTheKeyOnlyAsBearer(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("Authorization = %q", got)
		}
		if strings.Contains(r.URL.String(), testKey) {
			t.Errorf("key in URL %q", r.URL)
		}
		fmt.Fprint(w, `{"id":"u1","username":"me","mediaProgress":[{"libraryItemId":"i1","duration":100,"currentTime":5,"lastUpdate":1792787919031,"extra":true}]}`)
	})
	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if me.ID != "u1" || len(me.MediaProgress) != 1 || me.MediaProgress[0].CurrentTime != 5 {
		t.Fatalf("me = %+v", me)
	}
}

func TestClientRefusesRedirects(t *testing.T) {
	var followed bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed = true
	}))
	defer other.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/me", http.StatusFound)
	})
	if _, err := c.Me(context.Background()); err == nil {
		t.Fatal("a redirect was accepted")
	}
	if followed {
		t.Fatal("the redirect was followed, carrying the key elsewhere")
	}
}

func TestClientErrorsNeverCarryTheKey(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom: Bearer "+testKey)
	})
	_, err := c.Me(context.Background())
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v", err)
	}
}

// The error ends up in a PostgreSQL TEXT column, which refuses NUL and
// invalid UTF-8; the key straddles the 200-byte cut.
func TestClientErrorsAreStorableText(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "\x00bad\xff"+strings.Repeat("é", 95)+testKey)
	})
	_, err := c.Me(context.Background())
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	if !utf8.ValidString(msg) || strings.ContainsRune(msg, 0) || strings.Contains(msg, testKey[:4]) {
		t.Fatalf("err = %q", msg)
	}
}

func TestClientStatuses(t *testing.T) {
	for code, want := range map[int]error{401: ErrUnauthorized, 403: ErrUnauthorized, 404: ErrNotFound} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
		if _, err := c.Progress(context.Background(), "x"); !errors.Is(err, want) {
			t.Errorf("%d: err = %v, want %v", code, err, want)
		}
	}
}

func TestClientCapsTheBody(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"u1","username":"`)
		_, _ = io.CopyN(w, strings.NewReader(strings.Repeat("a", maxResponseBytes+10)), maxResponseBytes+10)
		fmt.Fprint(w, `"}`)
	})
	if _, err := c.Me(context.Background()); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateProgressSendsAndRefusesNonsense(t *testing.T) {
	var body string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.EscapedPath() != "/api/me/progress/i%2F1" {
			t.Errorf("%s %s", r.Method, r.URL.EscapedPath())
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	})
	if err := c.UpdateProgress(context.Background(), "i/1", ProgressUpdate{CurrentTime: 10, Duration: 100, Progress: 0.1}); err != nil {
		t.Fatal(err)
	}
	if body != `{"currentTime":10,"duration":100,"progress":0.1,"isFinished":false}` {
		t.Fatalf("body = %s", body)
	}
	if err := c.UpdateProgress(context.Background(), "i", ProgressUpdate{CurrentTime: 200, Duration: 100}); err == nil {
		t.Fatal("past-the-end time was written")
	}
}

func TestItemReadsChaptersAndAuthors(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("expanded") != "1" {
			t.Errorf("not expanded: %s", r.URL)
		}
		fmt.Fprint(w, `{"id":"i1","mediaType":"book","media":{"duration":60,
			"metadata":{"title":"Disquiet Gods: The Sun Eater: Book Six","authors":[{"name":"Christopher Ruocchio"}]},
			"chapters":[{"start":0,"end":30,"title":"Chapter 01","id":0},{"start":30,"end":60,"title":"Chapter 02","id":1}]}}`)
	})
	it, err := c.Item(context.Background(), "i1")
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Chapters) != 2 || it.Chapters[1].Start != 30 || len(it.Authors) != 1 || it.Authors[0] != "Christopher Ruocchio" {
		t.Fatalf("item = %+v", it)
	}
}

func TestItemsPagesBookLibrariesOnly(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/libraries":
			fmt.Fprint(w, `{"libraries":[{"id":"pods","mediaType":"podcast"},{"id":"books","mediaType":"book"}]}`)
		case "/api/libraries/books/items":
			if r.URL.Query().Get("page") == "0" {
				var res []string
				for i := range 100 {
					res = append(res, fmt.Sprintf(`{"id":"b%d","mediaType":"book","media":{"duration":10,"metadata":{"title":"T","authorName":"A, B"}}}`, i))
				}
				fmt.Fprintf(w, `{"total":101,"results":[%s]}`, strings.Join(res, ","))
				return
			}
			fmt.Fprint(w, `{"total":101,"results":[{"id":"last","mediaType":"book","media":{"duration":10,"metadata":{"title":"T"}}}]}`)
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	})
	items, err := c.Items(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 101 || items[100].ID != "last" || len(items[0].Authors) != 2 {
		t.Fatalf("got %d items, first %+v", len(items), items[0])
	}
}

func TestProgressValidate(t *testing.T) {
	now := time.UnixMilli(1792787919031)
	ok := Progress{LibraryItemID: "i", Duration: 100, CurrentTime: 10, LastUpdate: now.UnixMilli()}
	if err := ok.Validate(now); err != nil {
		t.Fatal(err)
	}
	ep := "e1"
	for name, mutate := range map[string]func(*Progress){
		"no item":      func(p *Progress) { p.LibraryItemID = "" },
		"episode":      func(p *Progress) { p.EpisodeID = &ep },
		"bad fraction": func(p *Progress) { p.Progress = 2 },
		"no duration":  func(p *Progress) { p.Duration = 0 },
		"negative":     func(p *Progress) { p.CurrentTime = -1 },
		"past end":     func(p *Progress) { p.CurrentTime = 200 },
		"ancient":      func(p *Progress) { p.LastUpdate = 1000 },
		"future":       func(p *Progress) { p.LastUpdate = now.Add(time.Hour).UnixMilli() },
	} {
		p := ok
		mutate(&p)
		if p.Validate(now) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestPeerIdentityIgnoresTrailingSlash(t *testing.T) {
	a := PeerIdentity("https://ab.example/", "u")
	if a != PeerIdentity("https://ab.example", "u") || a == PeerIdentity("https://ab.example", "v") || len(a) != 16 {
		t.Fatalf("identity %q", a)
	}
}

func TestSessionValidateDistinguishesFutureFromMalformed(t *testing.T) {
	now := time.UnixMilli(1792787919031)
	valid := Session{ID: "s", LibraryItemID: "i", TimeListening: 60, StartTime: 10, CurrentTime: 70,
		StartedAt: now.Add(-time.Minute).UnixMilli(), UpdatedAt: now.UnixMilli()}
	for name, tc := range map[string]struct {
		mutate func(*Session)
		want   error
	}{
		"valid":                {func(*Session) {}, nil},
		"no session":           {func(s *Session) { s.ID = "" }, ErrMalformedSession},
		"no item":              {func(s *Session) { s.LibraryItemID = "" }, ErrMalformedSession},
		"nul in id":            {func(s *Session) { s.ID = "s\x00" }, ErrMalformedSession},
		"invalid utf-8 device": {func(s *Session) { s.DeviceInfo.DeviceID = "d\xff" }, ErrMalformedSession},
		"over one day":         {func(s *Session) { s.TimeListening = 86401 }, ErrMalformedSession},
		"over span":            {func(s *Session) { s.TimeListening = 121 }, ErrMalformedSession},
		"timestamp overflow":   {func(s *Session) { s.UpdatedAt = math.MaxInt64 }, ErrMalformedSession},
		"nan listening":        {func(s *Session) { s.TimeListening = math.NaN() }, ErrMalformedSession},
		"infinite position":    {func(s *Session) { s.CurrentTime = math.Inf(1) }, ErrMalformedSession},
		"negative listening":   {func(s *Session) { s.TimeListening = -1 }, ErrMalformedSession},
		"negative time":        {func(s *Session) { s.StartTime = -1 }, ErrMalformedSession},
		"ancient":              {func(s *Session) { s.StartedAt = 1000 }, ErrMalformedSession},
		"reversed":             {func(s *Session) { s.UpdatedAt = s.StartedAt - 1 }, ErrMalformedSession},
		"future":               {func(s *Session) { s.UpdatedAt = now.Add(time.Hour).UnixMilli() }, ErrFutureSession},
		"fast clock":           {func(s *Session) { s.UpdatedAt = now.Add(maxClockSkew).UnixMilli() }, nil},
		"malformed and future": {func(s *Session) { s.TimeListening = -1; s.UpdatedAt = now.Add(time.Hour).UnixMilli() }, ErrMalformedSession},
	} {
		t.Run(name, func(t *testing.T) {
			s := valid
			tc.mutate(&s)
			if err := s.Validate(now); !errors.Is(err, tc.want) {
				t.Fatalf("Validate = %v, want %v", err, tc.want)
			}
		})
	}
	future := valid
	future.UpdatedAt = now.Add(time.Hour).UnixMilli()
	if err := future.Validate(now.Add(time.Hour)); err != nil {
		t.Fatalf("future session did not become valid: %v", err)
	}
}
