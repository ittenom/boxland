package designer_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	designerhandlers "boxland/server/internal/designer"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://boxland:boxland_dev@localhost:5433/boxland?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("postgres unavailable: %v", err)
	}
	return pool
}

func resetDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	wipe := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM designer_ws_tickets`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM designer_sessions`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM designers`)
	}
	wipe()
	t.Cleanup(wipe)
}

// buildHandler wires the same middleware stack the production binary uses,
// so handler tests exercise auth/session loading exactly as live requests do.
func buildHandler(deps designerhandlers.Deps) http.Handler {
	return designerhandlers.LoadSession(deps)(designerhandlers.New(deps))
}

func TestPostWSTicket_HappyPath(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)

	ctx := context.Background()
	auth := authdesigner.New(pool)
	d, _ := auth.CreateDesigner(ctx, "ws-ticket-handler@x.com", "p", authdesigner.RoleEditor)
	tok, _ := auth.OpenSession(ctx, d.ID, "test-ua", net.ParseIP("127.0.0.1"))

	srv := buildHandler(designerhandlers.Deps{Auth: auth})
	req := httptest.NewRequest(http.MethodPost, "/design/ws-ticket", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.AddCookie(&http.Cookie{Name: designerhandlers.SessionCookieName, Value: tok})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Ticket == "" {
		t.Error("ticket should be present in response")
	}

	// Ticket should redeem successfully.
	if _, err := auth.RedeemWSTicket(ctx, body.Ticket, net.ParseIP("127.0.0.1")); err != nil {
		t.Errorf("RedeemWSTicket: %v", err)
	}
}

func TestPostWSTicket_NoCookieIs401(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)

	srv := buildHandler(designerhandlers.Deps{Auth: authdesigner.New(pool)})
	req := httptest.NewRequest(http.MethodPost, "/design/ws-ticket", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
}

func TestPostWSTicket_BadCookieIs401(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)

	srv := buildHandler(designerhandlers.Deps{Auth: authdesigner.New(pool)})
	req := httptest.NewRequest(http.MethodPost, "/design/ws-ticket", nil)
	req.AddCookie(&http.Cookie{Name: designerhandlers.SessionCookieName, Value: "garbage"})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
}

// ---- Login / signup / logout flow ----

func TestSignupCreatesOwnerForFirstAccount(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	auth := authdesigner.New(pool)
	srv := buildHandler(designerhandlers.Deps{Auth: auth})

	form := strings.NewReader("email=first@x.com&password=hunter2!")
	req := httptest.NewRequest(http.MethodPost, "/design/signup", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/design/" {
		t.Errorf("redirect: got %q", rr.Header().Get("Location"))
	}
	d, err := auth.FindByEmail(context.Background(), "first@x.com")
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if d.Role != authdesigner.RoleOwner {
		t.Errorf("first account should be Owner, got %q", d.Role)
	}

	// Second signup should be Editor.
	form2 := strings.NewReader("email=second@x.com&password=hunter2!")
	req2 := httptest.NewRequest(http.MethodPost, "/design/signup", form2)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.RemoteAddr = "127.0.0.1:1"
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req2)
	d2, _ := auth.FindByEmail(context.Background(), "second@x.com")
	if d2.Role != authdesigner.RoleEditor {
		t.Errorf("second account should be Editor, got %q", d2.Role)
	}
}

func TestSignupShortPasswordRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	srv := buildHandler(designerhandlers.Deps{Auth: authdesigner.New(pool)})

	form := strings.NewReader("email=short@x.com&password=tiny")
	req := httptest.NewRequest(http.MethodPost, "/design/signup", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "8 characters") {
		t.Errorf("expected error in body, got: %s", rr.Body.String())
	}
}

func TestLoginAndLogoutFlow(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	auth := authdesigner.New(pool)
	srv := buildHandler(designerhandlers.Deps{Auth: auth})
	ctx := context.Background()

	// Pre-create the account.
	_, _ = auth.CreateDesigner(ctx, "log@x.com", "right-password-12", authdesigner.RoleEditor)

	// Wrong password → 401.
	bad := httptest.NewRequest(http.MethodPost, "/design/login",
		strings.NewReader("email=log@x.com&password=wrong-password"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bad.RemoteAddr = "127.0.0.1:1"
	badRR := httptest.NewRecorder()
	srv.ServeHTTP(badRR, bad)
	if badRR.Code != http.StatusUnauthorized {
		t.Errorf("wrong pwd: got %d, want 401", badRR.Code)
	}

	// Right password → redirect + cookie.
	good := httptest.NewRequest(http.MethodPost, "/design/login",
		strings.NewReader("email=log@x.com&password=right-password-12"))
	good.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	good.RemoteAddr = "127.0.0.1:1"
	goodRR := httptest.NewRecorder()
	srv.ServeHTTP(goodRR, good)
	if goodRR.Code != http.StatusSeeOther {
		t.Fatalf("login: got %d, body=%s", goodRR.Code, goodRR.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range goodRR.Result().Cookies() {
		if c.Name == designerhandlers.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("expected session cookie to be set")
	}

	// Authenticated GET /design/ → renders shell.
	homeReq := httptest.NewRequest(http.MethodGet, "/design/", nil)
	homeReq.AddCookie(sessionCookie)
	homeRR := httptest.NewRecorder()
	srv.ServeHTTP(homeRR, homeReq)
	if homeRR.Code != http.StatusOK {
		t.Errorf("shell: got %d, body=%s", homeRR.Code, homeRR.Body.String())
	}
	if !strings.Contains(homeRR.Body.String(), "log@x.com") {
		t.Errorf("shell did not render designer email")
	}

	// Logout → redirect, cookie cleared.
	out := httptest.NewRequest(http.MethodPost, "/design/logout", nil)
	out.AddCookie(sessionCookie)
	outRR := httptest.NewRecorder()
	srv.ServeHTTP(outRR, out)
	if outRR.Code != http.StatusSeeOther {
		t.Errorf("logout: got %d", outRR.Code)
	}

	// After logout, the same cookie no longer authenticates. Browser-style
	// request → redirect to login; non-HTML clients would get 401.
	again := httptest.NewRequest(http.MethodGet, "/design/", nil)
	again.Header.Set("Accept", "text/html")
	again.AddCookie(sessionCookie)
	againRR := httptest.NewRecorder()
	srv.ServeHTTP(againRR, again)
	if againRR.Code != http.StatusSeeOther {
		t.Errorf("post-logout shell: got %d, want 303 redirect to /design/login", againRR.Code)
	}
}

func TestUnauthShellRedirectsToLogin(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	srv := buildHandler(designerhandlers.Deps{Auth: authdesigner.New(pool)})

	req := httptest.NewRequest(http.MethodGet, "/design/", nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d, want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/design/login" {
		t.Errorf("redirect: got %q", rr.Header().Get("Location"))
	}
}
