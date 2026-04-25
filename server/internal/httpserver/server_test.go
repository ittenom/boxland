package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(ctx context.Context) error { return f.err }

func TestHealthzAllOK(t *testing.T) {
	srv := New(Health{
		Postgres: fakePinger{},
		Redis:    fakePinger{},
	}, Mounts{})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body healthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK {
		t.Error("expected OK=true")
	}
	if body.Services["postgres"] != "ok" || body.Services["redis"] != "ok" {
		t.Errorf("expected ok statuses, got %+v", body.Services)
	}
}

func TestHealthzDependencyDown(t *testing.T) {
	srv := New(Health{
		Postgres: fakePinger{err: errors.New("connection refused")},
		Redis:    fakePinger{},
	}, Mounts{})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var body healthResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.OK {
		t.Error("expected OK=false")
	}
	if body.Services["postgres"] == "ok" {
		t.Errorf("postgres should be down: %v", body.Services["postgres"])
	}
}

func TestHealthzNilPingerIsSkipped(t *testing.T) {
	srv := New(Health{}, Mounts{}) // both nil
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("nil pingers should still produce 200, got %d", rr.Code)
	}
	var body healthResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Services["postgres"] != "skipped" || body.Services["redis"] != "skipped" {
		t.Errorf("expected skipped, got %+v", body.Services)
	}
}

func TestLandingPage(t *testing.T) {
	srv := New(Health{}, Mounts{})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("landing /: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !contains(got, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html", got)
	}
	body := rr.Body.String()
	for _, want := range []string{"Boxland", "/design/login", "/play/login", "/healthz"} {
		if !contains(body, want) {
			t.Errorf("landing body missing %q", want)
		}
	}
}

// Landing page only matches the bare "/" path; nested unknowns 404.
func TestLandingDoesNotCatchAll(t *testing.T) {
	srv := New(Health{}, Mounts{})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("/nope: got %d, want 404", rr.Code)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}
