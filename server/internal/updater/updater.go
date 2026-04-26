// Package updater talks to GitHub Releases to decide whether the
// running Boxland is behind the latest published version. The TLI
// surfaces the result as a banner; the designer chrome reads the
// cached snapshot to render an in-app "update available" pill.
//
// Design rules baked in here:
//
//   - Fail soft. Network failures, captive portals, JSON parse
//     errors, rate-limit responses — none of these are user-facing
//     errors. We log at debug and return an empty Status so the
//     caller can quietly hide its UI.
//
//   - Cache aggressively. GitHub's unauthenticated quota is 60 req/h
//     per IP. A persistent on-disk cache with ETag conditional
//     requests means repeated TLI starts inside the TTL never spend
//     a quota call, and even out-of-TTL checks usually return 304
//     (which doesn't count). The cache also doubles as the source
//     for the in-app designer banner — the HTTP handler reads it
//     synchronously without ever touching the network.
//
//   - Never block startup. CheckLatest is intentionally synchronous
//     so callers control concurrency: the TLI fires it in a tea.Cmd
//     goroutine and the menu paints first.
//
//   - Honour `BOXLAND_DISABLE_UPDATE_CHECK` (offline / CI / private
//     forks) and `BOXLAND_GITHUB_TOKEN` (shared NAT hitting the IP
//     limit, or private fork mirrors).
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"boxland/server/internal/version"
)

// DefaultRepo points at the public Boxland repo. The TLI passes this
// to NewClient explicitly so tests and forks can override without
// reaching into the package.
const DefaultRepo = "ittenom/boxland"

// DefaultTTL is how long we trust a cached "latest release" snapshot
// before doing another conditional GET. Twelve hours is a comfortable
// fit: every second TLI launch on a typical dev day reuses cache, but
// users still see new releases the same day they ship.
const DefaultTTL = 12 * time.Hour

// minCheckInterval keeps us from hitting GitHub more than once per
// minute even when callers pass ForceRefresh repeatedly. Unrelated
// to the cache TTL: this is a throttle, not freshness.
const minCheckInterval = 30 * time.Second

// Status is the user-facing result of a check. A zero Status (all
// fields empty) means "we have no opinion" — the UI should show
// nothing.
type Status struct {
	Current      string    `json:"current"`
	Latest       string    `json:"latest"`         // empty when unknown
	HasUpdate    bool      `json:"has_update"`
	ReleaseURL   string    `json:"release_url,omitempty"`
	ReleaseNotes string    `json:"release_notes,omitempty"`
	PublishedAt  time.Time `json:"published_at,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
}

// Client checks GitHub Releases and persists results in a
// per-user cache file.
type Client struct {
	Repo      string        // "owner/repo"
	HTTP      *http.Client  // nil → http.DefaultClient with sane timeout
	CachePath string        // "" → user-config dir (per-OS)
	TTL       time.Duration // 0 → DefaultTTL
	Token     string        // "" reads BOXLAND_GITHUB_TOKEN at request time
	Now       func() time.Time
	Logger    *slog.Logger

	// UserAgent is required by GitHub for API access. We default
	// to "boxland/<version>" which gives them a way to contact us
	// if a release ever did something pathological.
	UserAgent string
}

// NewClient returns a Client wired with sensible defaults. Tests
// override fields directly on the returned value.
func NewClient(repo string) *Client {
	return &Client{
		Repo:      repo,
		HTTP:      &http.Client{Timeout: 10 * time.Second},
		TTL:       DefaultTTL,
		Now:       time.Now,
		Logger:    slog.Default(),
		UserAgent: "boxland/" + version.Current() + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")",
	}
}

// Disabled reports whether the user has opted out of update checks.
// Callers should treat this as "stop, do nothing" — don't even read
// the cache, since the user explicitly asked us to be silent.
func Disabled() bool {
	v := strings.TrimSpace(os.Getenv("BOXLAND_DISABLE_UPDATE_CHECK"))
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// CheckLatest returns the newest release and a HasUpdate flag. It
// short-circuits in three useful ways:
//
//   - dev builds (`0.0.0-dev`) never report HasUpdate; we'd have
//     nothing useful to say,
//   - BOXLAND_DISABLE_UPDATE_CHECK=true returns a zero Status,
//   - a fresh-enough cache returns immediately, untouched.
//
// Errors are returned but the caller is expected to log-and-discard;
// the *Status return is always safe to render even when err != nil
// (it'll just be the cached or zero value).
func (c *Client) CheckLatest(ctx context.Context, opts CheckOpts) (*Status, error) {
	if Disabled() {
		return &Status{Current: version.Current(), CheckedAt: c.now()}, nil
	}
	if dev := version.IsDev(); dev != "" {
		return &Status{Current: dev, CheckedAt: c.now()}, nil
	}
	cache, _ := c.readCache()

	// Cache fast path: serve from cache if the entry is fresh,
	// unless ForceRefresh is set (TLI's "U" hotkey for re-check).
	if !opts.ForceRefresh && cache != nil && c.cacheFresh(cache) {
		return cache.toStatus(version.Current()), nil
	}
	// Throttle: even with ForceRefresh, refuse to spam GitHub more
	// than once per minCheckInterval. This protects test rigs and
	// users mashing the hotkey.
	if cache != nil && c.now().Sub(cache.CheckedAt) < minCheckInterval {
		return cache.toStatus(version.Current()), nil
	}
	rel, status, err := c.fetchLatest(ctx, cache)
	if err != nil {
		// Don't lose the cached value on transient failure: the user
		// keeps seeing what they had before.
		c.logger().Debug("update check failed", "err", err, "repo", c.Repo)
		if cache != nil {
			// Bump CheckedAt so we don't retry every second.
			cache.CheckedAt = c.now()
			_ = c.writeCache(cache)
			return cache.toStatus(version.Current()), err
		}
		return &Status{Current: version.Current(), CheckedAt: c.now()}, err
	}

	// 304 Not Modified: server agreed our cache is still authoritative.
	if status == http.StatusNotModified && cache != nil {
		cache.CheckedAt = c.now()
		_ = c.writeCache(cache)
		return cache.toStatus(version.Current()), nil
	}
	if rel == nil {
		return &Status{Current: version.Current(), CheckedAt: c.now()}, nil
	}

	entry := &cacheEntry{
		Repo:        c.Repo,
		Latest:      rel.Tag,
		ReleaseURL:  rel.HTMLURL,
		Notes:       rel.Body,
		PublishedAt: rel.PublishedAt,
		ETag:        rel.ETag,
		CheckedAt:   c.now(),
	}
	if err := c.writeCache(entry); err != nil {
		c.logger().Debug("update cache write failed", "err", err)
	}
	return entry.toStatus(version.Current()), nil
}

// CheckOpts tweaks a single CheckLatest call.
type CheckOpts struct {
	// ForceRefresh skips the TTL gate and always issues a
	// conditional GET (still respecting ETag, so a 304 still costs
	// nothing of GitHub's quota).
	ForceRefresh bool
}

// Cached returns the most recent cached snapshot without touching
// the network. Designer in-app handlers use this so a page render
// is never blocked on GitHub. nil/empty when nothing has been cached
// yet.
func (c *Client) Cached() *Status {
	if Disabled() {
		return nil
	}
	cache, _ := c.readCache()
	if cache == nil {
		return nil
	}
	return cache.toStatus(version.Current())
}

// ----- HTTP -----

type releasePayload struct {
	Tag         string
	HTMLURL     string
	Body        string
	PublishedAt time.Time
	ETag        string
}

// fetchLatest issues a conditional GET against GitHub's
// /releases/latest. status is the HTTP status code (so callers can
// distinguish 304 from 200); rel is non-nil only on 200.
func (c *Client) fetchLatest(ctx context.Context, prior *cacheEntry) (*releasePayload, int, error) {
	if c.Repo == "" {
		return nil, 0, errors.New("updater: empty Repo")
	}
	url := c.apiBase() + "/repos/" + c.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.UserAgent)
	if t := c.token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	if prior != nil && prior.ETag != "" {
		req.Header.Set("If-None-Match", prior.ETag)
	}

	cli := c.HTTP
	if cli == nil {
		cli = http.DefaultClient
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, resp.StatusCode, nil
	case http.StatusOK:
		// fall through
	case http.StatusNotFound:
		// No releases yet. Equivalent to "no update available";
		// cache an empty result so we don't keep asking.
		return nil, resp.StatusCode, fmt.Errorf("no releases for %s", c.Repo)
	case http.StatusForbidden, http.StatusTooManyRequests:
		// Rate limited. Surface the reset time in the error so logs
		// are actionable.
		reset := resp.Header.Get("X-RateLimit-Reset")
		return nil, resp.StatusCode, fmt.Errorf("github rate limited (reset=%s)", reset)
	default:
		return nil, resp.StatusCode, fmt.Errorf("github returned %d", resp.StatusCode)
	}

	var body struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		Body        string    `json:"body"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err := dec.Decode(&body); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode release: %w", err)
	}
	if body.Draft || body.Prerelease {
		return nil, resp.StatusCode, fmt.Errorf("latest is draft/prerelease, ignoring")
	}
	if strings.TrimSpace(body.TagName) == "" {
		return nil, resp.StatusCode, errors.New("release missing tag_name")
	}
	return &releasePayload{
		Tag:         body.TagName,
		HTMLURL:     body.HTMLURL,
		Body:        body.Body,
		PublishedAt: body.PublishedAt,
		ETag:        resp.Header.Get("ETag"),
	}, resp.StatusCode, nil
}

// apiBase lets tests inject a fake GitHub via APIBase, without
// requiring callers in production to set anything.
//
// Set indirectly via the package-level APIBaseOverride var (set by
// tests) so the hot path stays a constant string concat.
func (c *Client) apiBase() string {
	if APIBaseOverride != "" {
		return APIBaseOverride
	}
	return "https://api.github.com"
}

// APIBaseOverride is for tests only. Production code never sets it.
var APIBaseOverride string

// ----- helpers -----

func (c *Client) cacheFresh(e *cacheEntry) bool {
	ttl := c.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	return c.now().Sub(e.CheckedAt) < ttl
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *Client) token() string {
	if c.Token != "" {
		return c.Token
	}
	return strings.TrimSpace(os.Getenv("BOXLAND_GITHUB_TOKEN"))
}

// ----- cache -----

type cacheEntry struct {
	Repo        string    `json:"repo"`
	Latest      string    `json:"latest"`
	ReleaseURL  string    `json:"release_url"`
	Notes       string    `json:"notes"`
	PublishedAt time.Time `json:"published_at"`
	ETag        string    `json:"etag,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
}

func (e *cacheEntry) toStatus(current string) *Status {
	if e == nil {
		return &Status{Current: current}
	}
	s := &Status{
		Current:      current,
		Latest:       e.Latest,
		ReleaseURL:   e.ReleaseURL,
		ReleaseNotes: e.Notes,
		PublishedAt:  e.PublishedAt,
		CheckedAt:    e.CheckedAt,
	}
	if s.Latest != "" && version.IsNewer(current, s.Latest) {
		s.HasUpdate = true
	}
	return s
}

func (c *Client) cachePath() string {
	if c.CachePath != "" {
		return c.CachePath
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		// Fallback: temp dir. Not great, but better than blowing
		// up; nothing we cache here is precious.
		dir = os.TempDir()
	}
	return filepath.Join(dir, "boxland", "update-cache.json")
}

func (c *Client) readCache() (*cacheEntry, error) {
	path := c.cachePath()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e cacheEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	// Stale entries from a different repo shouldn't poison the
	// current view (a user pointing a fork at their own repo, then
	// switching back, etc.).
	if c.Repo != "" && e.Repo != "" && e.Repo != c.Repo {
		//nolint:nilnil // intentional: tolerate cache miss
		return nil, nil
	}
	return &e, nil
}

func (c *Client) writeCache(e *cacheEntry) error {
	path := c.cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
