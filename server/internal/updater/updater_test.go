package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"boxland/server/internal/version"
)

// fakeGitHub spins up an httptest.Server that serves /repos/.../
// releases/latest with the supplied tag and supports ETag conditional
// requests. Returns the server, the underlying request counter, and
// a setter to flip the tag mid-test.
func fakeGitHub(t *testing.T, initialTag string) (*httptest.Server, *atomic.Int64, func(string)) {
	t.Helper()
	var count atomic.Int64
	tag := atomic.Pointer[string]{}
	tag.Store(&initialTag)
	const etag = `W/"abc123"`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		t := *tag.Load()
		body, _ := json.Marshal(map[string]any{
			"tag_name":     t,
			"html_url":     "https://example.test/release/" + t,
			"body":         "Notes for " + t,
			"published_at": "2026-04-01T00:00:00Z",
		})
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &count, func(s string) { tag.Store(&s) }
}

// newTestClient returns a Client with a temp cache path, a sane
// frozen clock, and the override-API base pointing at srv.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	cache := filepath.Join(t.TempDir(), "update-cache.json")
	prev := APIBaseOverride
	APIBaseOverride = srv.URL
	t.Cleanup(func() { APIBaseOverride = prev })

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	c := NewClient("ittenom/boxland-test")
	c.CachePath = cache
	c.Now = func() time.Time { return now }
	return c
}

func TestCheckLatest_HasUpdate(t *testing.T) {
	srv, _, _ := fakeGitHub(t, "v999.0.0")
	c := newTestClient(t, srv)

	got, err := c.CheckLatest(context.Background(), CheckOpts{})
	if err != nil {
		t.Fatalf("CheckLatest err: %v", err)
	}
	if got == nil {
		t.Fatal("CheckLatest returned nil status")
	}
	if got.Current != version.Current() {
		t.Errorf("Current = %q, want %q", got.Current, version.Current())
	}
	if got.Latest != "v999.0.0" {
		t.Errorf("Latest = %q, want v999.0.0", got.Latest)
	}
	if !got.HasUpdate {
		t.Errorf("HasUpdate = false, want true (current %s vs v999.0.0)", got.Current)
	}
	if got.ReleaseURL == "" {
		t.Errorf("ReleaseURL is empty")
	}
}

func TestCheckLatest_NoUpdateWhenSameVersion(t *testing.T) {
	srv, _, _ := fakeGitHub(t, "v"+version.Current())
	c := newTestClient(t, srv)

	got, err := c.CheckLatest(context.Background(), CheckOpts{})
	if err != nil {
		t.Fatalf("CheckLatest err: %v", err)
	}
	if got.HasUpdate {
		t.Errorf("HasUpdate = true with same version (%s)", got.Latest)
	}
}

func TestCheckLatest_CacheServesFresh(t *testing.T) {
	srv, count, _ := fakeGitHub(t, "v999.0.0")
	c := newTestClient(t, srv)

	if _, err := c.CheckLatest(context.Background(), CheckOpts{}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	first := count.Load()
	if first != 1 {
		t.Fatalf("first call hit upstream %d times, want 1", first)
	}
	// Inside TTL: should serve from cache without contacting upstream.
	for i := 0; i < 5; i++ {
		if _, err := c.CheckLatest(context.Background(), CheckOpts{}); err != nil {
			t.Fatalf("cached call: %v", err)
		}
	}
	if got := count.Load(); got != 1 {
		t.Errorf("cached calls hit upstream; count = %d, want 1", got)
	}
}

func TestCheckLatest_ForceRefreshUsesETag(t *testing.T) {
	srv, count, _ := fakeGitHub(t, "v999.0.0")
	c := newTestClient(t, srv)
	// Stretch minCheckInterval out of the way by advancing the clock
	// each call.
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }

	if _, err := c.CheckLatest(context.Background(), CheckOpts{}); err != nil {
		t.Fatalf("first: %v", err)
	}
	first := count.Load()

	now = now.Add(2 * time.Minute)
	got, err := c.CheckLatest(context.Background(), CheckOpts{ForceRefresh: true})
	if err != nil {
		t.Fatalf("force: %v", err)
	}
	if count.Load() != first+1 {
		t.Errorf("ForceRefresh did not call upstream; count=%d", count.Load())
	}
	if got.Latest != "v999.0.0" {
		t.Errorf("Latest = %q after 304", got.Latest)
	}
}

func TestCheckLatest_NetworkErrorReturnsCachedStatus(t *testing.T) {
	srv, _, _ := fakeGitHub(t, "v999.0.0")
	c := newTestClient(t, srv)
	if _, err := c.CheckLatest(context.Background(), CheckOpts{}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Tear down upstream and force a refresh past the throttle
	// window. Advance the test clock relative to the seed time, NOT
	// real time — newTestClient froze us at 2026-05-01 12:00 UTC,
	// and the throttle compares the cache's CheckedAt against the
	// client's clock.
	srv.Close()
	now := time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC) // +1 hour
	c.Now = func() time.Time { return now }
	got, err := c.CheckLatest(context.Background(), CheckOpts{ForceRefresh: true})
	if err == nil {
		t.Fatalf("expected error after server torn down (got status: %+v)", got)
	}
	if got == nil || got.Latest != "v999.0.0" {
		t.Fatalf("cached value lost on transient failure: got %+v", got)
	}
}

func TestDisabledShortCircuits(t *testing.T) {
	t.Setenv("BOXLAND_DISABLE_UPDATE_CHECK", "true")
	srv, count, _ := fakeGitHub(t, "v999.0.0")
	c := newTestClient(t, srv)
	got, err := c.CheckLatest(context.Background(), CheckOpts{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if count.Load() != 0 {
		t.Errorf("network was contacted with disable=true")
	}
	if got == nil || got.Latest != "" || got.HasUpdate {
		t.Errorf("disabled status not blank: %+v", got)
	}
}

func TestRateLimitedSurfacedAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Reset", "9999999999")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	prev := APIBaseOverride
	APIBaseOverride = srv.URL
	t.Cleanup(func() { APIBaseOverride = prev })

	c := NewClient("ittenom/boxland")
	c.CachePath = filepath.Join(t.TempDir(), "cache.json")
	got, err := c.CheckLatest(context.Background(), CheckOpts{})
	if err == nil {
		t.Fatalf("rate-limit response should produce an error")
	}
	if got == nil {
		t.Fatalf("status should still be non-nil on rate-limit")
	}
}

func TestCachedReturnsNilWhenNoEntry(t *testing.T) {
	c := NewClient("x/y")
	c.CachePath = filepath.Join(t.TempDir(), "missing.json")
	if got := c.Cached(); got != nil {
		t.Errorf("Cached on empty path = %+v, want nil", got)
	}
}

func TestCachedReadsAfterCheck(t *testing.T) {
	srv, _, _ := fakeGitHub(t, "v999.0.0")
	c := newTestClient(t, srv)
	if _, err := c.CheckLatest(context.Background(), CheckOpts{}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := c.Cached()
	if got == nil || got.Latest != "v999.0.0" {
		t.Errorf("Cached after seed = %+v", got)
	}
}

func TestCacheRepoMismatchIgnored(t *testing.T) {
	srv, _, _ := fakeGitHub(t, "v999.0.0")
	c := newTestClient(t, srv)
	if _, err := c.CheckLatest(context.Background(), CheckOpts{}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Same cache file, different repo. Should treat as a miss.
	c2 := NewClient("someone/else")
	c2.CachePath = c.CachePath
	if got := c2.Cached(); got != nil {
		t.Errorf("foreign-repo cache leaked: %+v", got)
	}
}
