package playerweb_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/auth/csrf"
	authplayer "boxland/server/internal/auth/player"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/playerweb"
)

// openPool returns an isolated, freshly-migrated DB. testdb.New wires its own t.Cleanup that drops the database when the test ends.
func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// fixture spins up the playerweb mux with CSRF + LoadSession middleware
// (matching the production wiring in cmd/boxland/main.go).
type fixture struct {
	t       *testing.T
	pool    *pgxpool.Pool
	srv     *httptest.Server
	authSvc *authplayer.Service
	mapsSvc *maps.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := openPool(t)
	t.Cleanup(pool.Close)

	authP := authplayer.New(pool, []byte("test-jwt-secret-32-bytes-padded__"))
	mapsSvc := maps.New(pool)

	deps := playerweb.Deps{
		Auth:          authP,
		Maps:          mapsSvc,
		SecureCookies: false,
	}
	csrfMW := csrf.Middleware(csrf.Config{Secure: false, SameSite: http.SameSiteStrictMode})
	loadMW := playerweb.LoadSession(deps)
	mux := csrfMW(loadMW(playerweb.New(deps)))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &fixture{t: t, pool: pool, srv: srv, authSvc: authP, mapsSvc: mapsSvc}
}

// httpClient returns a client that follows no redirects so we can assert
// on the 303-redirect status the auth handlers emit.
func noRedirectClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// primeCSRF performs a GET against the server so the CSRF middleware
// mints + sets its cookie on the jar, then returns the token value so
// callers can echo it as X-CSRF-Token on later POSTs. Mirrors what the
// JS shim in static/js/boot.js does in the browser.
func primeCSRF(t *testing.T, c *http.Client, baseURL string) string {
	t.Helper()
	resp, err := c.Get(baseURL + "/play/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == csrf.CookieName {
			return ck.Value
		}
	}
	// Fallback: middleware always sets the cookie, but the headers may
	// not surface it on the second hop; query the jar directly.
	if jar, ok := c.Jar.(*simpleJar); ok {
		if ck, ok := jar.m[csrf.CookieName]; ok {
			return ck.Value
		}
	}
	t.Fatal("primeCSRF: no csrf cookie set")
	return ""
}

// postForm posts a form with the X-CSRF-Token header. Returns the response.
func postForm(t *testing.T, c *http.Client, target, csrfTok string, form url.Values) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(csrf.HeaderName, csrfTok)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func newJar() http.CookieJar {
	jar, _ := newCookieJar()
	return jar
}

func newCookieJar() (http.CookieJar, error) {
	// Tiny in-house jar: persist all cookies, ignore domain/path matching
	// rules so the test client always presents what the server set last.
	jar := &simpleJar{m: map[string]*http.Cookie{}}
	return jar, nil
}

type simpleJar struct {
	m map[string]*http.Cookie
}

func (j *simpleJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	for _, c := range cookies {
		if c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now())) {
			delete(j.m, c.Name)
		} else {
			j.m[c.Name] = c
		}
	}
}
func (j *simpleJar) Cookies(_ *url.URL) []*http.Cookie {
	out := make([]*http.Cookie, 0, len(j.m))
	for _, c := range j.m {
		out = append(out, c)
	}
	return out
}

// ---- Tests ----

func TestPlayerLogin_RedirectsToLoginWhenAnonymous(t *testing.T) {
	f := newFixture(t)
	c := noRedirectClient(newJar())
	resp, err := c.Get(f.srv.URL + "/play/maps")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status: got %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/play/login" {
		t.Errorf("Location: got %q, want /play/login", loc)
	}
}

func TestPlayerSignup_LandsOnMaps(t *testing.T) {
	f := newFixture(t)
	jar := newJar()
	c := noRedirectClient(jar)
	tok := primeCSRF(t, c, f.srv.URL)

	form := url.Values{"email": {"signup-test@x.com"}, "password": {"hunter2pass"}}
	resp := postForm(t, c, f.srv.URL+"/play/signup", tok, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d, want 303; body=%s", resp.StatusCode, body)
	}
	if loc := resp.Header.Get("Location"); loc != "/play/maps" {
		t.Errorf("Location: got %q, want /play/maps", loc)
	}

	// Cookie was set: a follow-up GET /play/maps should now be 200.
	resp2, err := c.Get(f.srv.URL + "/play/maps")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("after signup: status %d, want 200", resp2.StatusCode)
	}
}

func TestPlayerLogin_BadCredentialsRendersError(t *testing.T) {
	f := newFixture(t)
	// Pre-create a player so the email exists but password is wrong.
	if _, err := f.authSvc.CreatePlayer(context.Background(), "loginbad@x.com", "rightpass"); err != nil {
		t.Fatal(err)
	}
	c := noRedirectClient(newJar())
	tok := primeCSRF(t, c, f.srv.URL)
	form := url.Values{"email": {"loginbad@x.com"}, "password": {"wrong"}}
	resp := postForm(t, c, f.srv.URL+"/play/login", tok, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200 (form re-render)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Invalid email or password") {
		t.Errorf("expected error message; body=%s", body)
	}
}

func TestPlayerWSTicket_ReturnsJSONToken(t *testing.T) {
	f := newFixture(t)
	jar := newJar()
	c := noRedirectClient(jar)
	tok := primeCSRF(t, c, f.srv.URL)
	// Sign up to get a session.
	resp0 := postForm(t, c, f.srv.URL+"/play/signup", tok,
		url.Values{"email": {"ticketgen@x.com"}, "password": {"hunter2pass"}})
	resp0.Body.Close()

	req, _ := http.NewRequest("POST", f.srv.URL+"/play/ws-ticket", nil)
	req.Header.Set(csrf.HeaderName, tok)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, body)
	}
	var out struct{ Token string `json:"token"` }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" {
		t.Fatal("token: empty")
	}
	// The minted JWT should parse back to our player.
	claims, err := f.authSvc.ParseAccessToken(out.Token)
	if err != nil {
		t.Fatalf("ParseAccessToken: %v", err)
	}
	if claims.PlayerID == 0 {
		t.Errorf("PlayerID: got 0")
	}
}

func TestPlayerMaps_ListsOnlyPublic(t *testing.T) {
	f := newFixture(t)
	jar := newJar()
	c := noRedirectClient(jar)
	tok := primeCSRF(t, c, f.srv.URL)
	// Sign up + log in.
	resp0 := postForm(t, c, f.srv.URL+"/play/signup", tok,
		url.Values{"email": {"mapper@x.com"}, "password": {"hunter2pass"}})
	resp0.Body.Close()

	// Need a designer to create maps. The maps service requires a created_by;
	// we cheat by inserting a designer row via the maps service's CreateInput
	// with a sentinel id (matches the FK once the designer is created).
	// Easier: borrow the designer auth service.
	pool := f.pool
	_, err := pool.Exec(context.Background(), `
		INSERT INTO designers (email, password_hash, role) VALUES ($1, $2, 'editor')
	`, "designer-fixture@x.com", "x")
	if err != nil {
		t.Fatal(err)
	}
	var designerID int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM designers WHERE email = $1`,
		"designer-fixture@x.com").Scan(&designerID); err != nil {
		t.Fatal(err)
	}

	if _, err := f.mapsSvc.Create(context.Background(), maps.CreateInput{
		Name: "PublicLand", Width: 32, Height: 32, Public: true, CreatedBy: designerID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mapsSvc.Create(context.Background(), maps.CreateInput{
		Name: "PrivateZone", Width: 32, Height: 32, Public: false, CreatedBy: designerID,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := c.Get(f.srv.URL + "/play/maps")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "PublicLand") {
		t.Errorf("expected PublicLand in body; body=%s", body)
	}
	if strings.Contains(string(body), "PrivateZone") {
		t.Errorf("PrivateZone should NOT appear; body=%s", body)
	}
}

func TestPlayerGame_RejectsPrivateMapByID(t *testing.T) {
	f := newFixture(t)
	jar := newJar()
	c := noRedirectClient(jar)
	tok := primeCSRF(t, c, f.srv.URL)
	resp0 := postForm(t, c, f.srv.URL+"/play/signup", tok,
		url.Values{"email": {"sneaker@x.com"}, "password": {"hunter2pass"}})
	resp0.Body.Close()

	// Create a private map directly.
	pool := f.pool
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO designers (email, password_hash, role) VALUES ($1, $2, 'editor')
	`, "designer-private@x.com", "x"); err != nil {
		t.Fatal(err)
	}
	var did int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM designers WHERE email='designer-private@x.com'`).Scan(&did); err != nil {
		t.Fatal(err)
	}
	m, err := f.mapsSvc.Create(context.Background(), maps.CreateInput{
		Name: "Hidden", Width: 32, Height: 32, Public: false, CreatedBy: did,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.Get(f.srv.URL + "/play/game/" + itoa(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404 (private map)", resp.StatusCode)
	}
}

func TestPlayerLogout_ClearsSession(t *testing.T) {
	f := newFixture(t)
	jar := newJar()
	c := noRedirectClient(jar)
	tok := primeCSRF(t, c, f.srv.URL)
	resp0 := postForm(t, c, f.srv.URL+"/play/signup", tok,
		url.Values{"email": {"logout@x.com"}, "password": {"hunter2pass"}})
	resp0.Body.Close()
	resp := postForm(t, c, f.srv.URL+"/play/logout", tok, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status: got %d, want 303", resp.StatusCode)
	}
	// Subsequent /play/maps must redirect us to /play/login again.
	resp2, err := c.Get(f.srv.URL + "/play/maps")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusSeeOther {
		t.Errorf("after logout: got %d, want 303", resp2.StatusCode)
	}
}

// ---- helpers ----

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	out := ""
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		d := byte(i % 10)
		out = string(rune('0'+d)) + out
		i /= 10
	}
	if neg {
		out = "-" + out
	}
	return out
}
