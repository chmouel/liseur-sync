package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type metadataRoundTripper func(*http.Request) (*http.Response, error)

func (f metadataRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func metadataResponse(id, redirect string) *http.Response {
	body, _ := json.Marshal(map[string]any{"client_id": id, "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none"})
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}
}

func TestMetadataFetchCoalesces(t *testing.T) {
	f := NewMetadataFetcher()
	id := "https://client.example/metadata.json"
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	f.Client = &http.Client{Transport: metadataRoundTripper(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return metadataResponse(id, "https://client.example/callback"), nil
	})}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc, err := f.Fetch(t.Context(), id, "generation")
			if err != nil || len(doc.RedirectURIs) != 1 {
				t.Errorf("metadata: %+v %v", doc, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("fetches: %d", calls.Load())
	}
}

func TestMetadataForgetInvalidatesInFlightFetch(t *testing.T) {
	f := NewMetadataFetcher()
	id := "https://client.example/metadata.json"
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	f.Client = &http.Client{Transport: metadataRoundTripper(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return metadataResponse(id, "https://old.example/callback"), nil
		}
		return metadataResponse(id, "https://new.example/callback"), nil
	})}
	done := make(chan error, 1)
	go func() { _, err := f.Fetch(t.Context(), id, "generation"); done <- err }()
	<-started
	f.Forget(id)
	doc, err := f.Fetch(t.Context(), id, "generation")
	if err != nil || doc.RedirectURIs[0] != "https://new.example/callback" {
		t.Fatalf("fresh metadata: %+v %v", doc, err)
	}
	close(release)
	if err := <-done; err != errMetadataInvalidated {
		t.Fatalf("stale fetch: %v", err)
	}
	doc, err = f.Fetch(t.Context(), id, "generation")
	if err != nil || doc.RedirectURIs[0] != "https://new.example/callback" || calls.Load() != 2 {
		t.Fatalf("cached metadata: %+v %v, fetches %d", doc, err, calls.Load())
	}
}

func TestMetadataWaiterCancellation(t *testing.T) {
	f := NewMetadataFetcher()
	id := "https://client.example/metadata.json"
	started, release := make(chan struct{}), make(chan struct{})
	f.Client = &http.Client{Transport: metadataRoundTripper(func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return metadataResponse(id, "https://client.example/callback"), nil
	})}
	done := make(chan error, 1)
	go func() { _, err := f.Fetch(t.Context(), id, "generation"); done <- err }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.Fetch(ctx, id, "generation"); err != context.Canceled {
		t.Fatalf("canceled waiter: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMetadataURLRejectsInvalidAuthorityAndQuery(t *testing.T) {
	for _, value := range []string{"https://client.example/metadata?", "https://:443/metadata", "https://client.example/metadata#"} {
		if err := ValidateMetadataURL(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	if err := ValidateMetadataURL("https://client.example/metadata.json"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRedirectURI("https://:443/callback"); err == nil {
		t.Fatal("accepted redirect without hostname")
	}
}

func TestMetadataUpstreamTimeoutIsShared(t *testing.T) {
	f := NewMetadataFetcher()
	id := "https://client.example/metadata.json"
	pending := &metadataLoad{done: make(chan struct{}), err: context.DeadlineExceeded}
	close(pending.done)
	f.loading[metadataKey{url: id, generation: "generation"}] = pending
	if _, err := f.Fetch(t.Context(), id, "generation"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
}
