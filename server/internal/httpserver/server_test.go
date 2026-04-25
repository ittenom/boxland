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
