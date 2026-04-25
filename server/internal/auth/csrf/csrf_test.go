package csrf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := Token(r.Context())
		if tok == "" {
			t.Error("expected non-empty token in context")
		}
		_, _ = w.Write([]byte("ok:" + tok))
	})
}

func TestGetIsAlwaysAllowed_AndSetsCookie(t *testing.T) {
	mw := Middleware(DefaultConfig())(handler(t))
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	var got *http.Cookie
	for _, c := range cookies {
		if c.Name == CookieName {
			got = c
		}
	}
	if got == nil || got.Value == "" {
		t.Fatal("expected csrf cookie to be set on first GET")
	}
	if got.HttpOnly {
		t.Error("csrf cookie must be readable by JS (HttpOnly=false)")
	}
}

func TestPostWithoutHeaderRejected(t *testing.T) {
	mw := Middleware(DefaultConfig())(handler(t))

	// First GET to mint a cookie.
	getRR := httptest.NewRecorder()
	mw.ServeHTTP(getRR, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := getRR.Result().Cookies()[0]

	// POST that brings the cookie back but no header → 403.
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.AddCookie(cookie)
	postRR := httptest.NewRecorder()
	mw.ServeHTTP(postRR, req)

	if postRR.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", postRR.Code)
	}
}

func TestPostWithMatchingHeaderAccepted(t *testing.T) {
	mw := Middleware(DefaultConfig())(handler(t))

	getRR := httptest.NewRecorder()
	mw.ServeHTTP(getRR, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := getRR.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.AddCookie(cookie)
	req.Header.Set(HeaderName, cookie.Value)
	postRR := httptest.NewRecorder()
	mw.ServeHTTP(postRR, req)

	if postRR.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200; body=%s", postRR.Code, postRR.Body.String())
	}
}

func TestPostWithWrongHeaderRejected(t *testing.T) {
	mw := Middleware(DefaultConfig())(handler(t))

	getRR := httptest.NewRecorder()
	mw.ServeHTTP(getRR, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := getRR.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.AddCookie(cookie)
	req.Header.Set(HeaderName, "obviously-different")
	postRR := httptest.NewRecorder()
	mw.ServeHTTP(postRR, req)

	if postRR.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", postRR.Code)
	}
}

func TestIsSafeMethod(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if !IsSafeMethod(m) {
			t.Errorf("%s should be safe", m)
		}
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if IsSafeMethod(m) {
			t.Errorf("%s should be unsafe", m)
		}
	}
}
