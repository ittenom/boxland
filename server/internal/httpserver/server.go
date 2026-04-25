// Package httpserver is the public-facing HTTP entrypoint.
// Routes added incrementally as features land.
package httpserver

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	staticfs "boxland/server/static"
)

// Pinger is anything that can verify its own connectivity with a context.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Health holds the dependencies whose liveness contributes to /healthz.
// Each may be nil during early development; nil entries are skipped.
type Health struct {
	Postgres Pinger
	Redis    Pinger
}

// Mounts is the set of sub-routers to splice into the root tree. Each may
// be nil; the corresponding routes simply won't exist.
type Mounts struct {
	Designer http.Handler // /design/* and (later) /auth/designer/*
	Player   http.Handler // /play/* (player surface: login, server picker, map picker, game view)
	WS       http.Handler // /ws (single endpoint; realm decided by Auth handshake)
}

// New builds the http.Handler tree. Add routes here as packages come online.
func New(h Health, m Mounts) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(h))
	mux.HandleFunc("GET /{$}", landingHandler())
	mux.Handle("GET /static/", staticHandler())
	if m.Designer != nil {
		mux.Handle("/design/", m.Designer)
	}
	if m.Player != nil {
		mux.Handle("/play/", m.Player)
	}
	if m.WS != nil {
		// One canonical WS endpoint; realm is decided by the FlatBuffers
		// Auth handshake (PLAN.md §1 "WS auth realms").
		mux.Handle("GET /ws", m.WS)
	}
	return mux
}

// landingHandler serves the root "/" page so a user who just ran
// `just design` and navigated to http://localhost:8080/ lands on a
// real welcome screen instead of a 404. Two big cards: Design + Play.
//
// Using an inline html/template keeps this self-contained -- no
// extra Templ generation step, and the design-tools package's auth
// middleware doesn't need to know about a public landing page.
func landingHandler() http.HandlerFunc {
	const body = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover" />
<title>Boxland</title>
<link rel="stylesheet" href="/static/css/pixel.css" />
<style>
.bx-launchpad { max-width: 480px; margin: 64px auto; padding: 0 24px; text-align: center; }
.bx-launchpad__title { font-size: 36px; margin-bottom: 24px; }
.bx-launchpad__subtitle { margin-bottom: 32px; opacity: 0.85; }
.bx-launchpad__cards { display: grid; gap: 16px; grid-template-columns: 1fr 1fr; }
.bx-launchpad__card { display: block; padding: 24px; background: var(--bx-bg-2); border: 4px solid var(--bx-line); color: var(--bx-fg); text-decoration: none; transition: transform 60ms ease, border-color 60ms ease; }
.bx-launchpad__card:hover { border-color: var(--bx-accent); transform: translateY(-2px); }
.bx-launchpad__card-title { display: block; font-weight: 700; font-size: 18px; margin-bottom: 8px; }
.bx-launchpad__card-meta { display: block; font-size: 12px; opacity: 0.75; }
.bx-launchpad__health { margin-top: 32px; font-size: 12px; opacity: 0.6; }
</style>
</head>
<body data-surface="landing">
<main class="bx-launchpad">
<h1 class="bx-launchpad__title bx-mono">Boxland</h1>
<p class="bx-launchpad__subtitle">Welcome. Pick a side to start.</p>
<nav class="bx-launchpad__cards">
<a class="bx-launchpad__card" href="/design/login">
<span class="bx-launchpad__card-title">Design</span>
<span class="bx-launchpad__card-meta">Make assets, entities, maps, and worlds.</span>
</a>
<a class="bx-launchpad__card" href="/play/login">
<span class="bx-launchpad__card-title">Play</span>
<span class="bx-launchpad__card-meta">Drop into a public map.</span>
</a>
</nav>
<p class="bx-launchpad__health"><a href="/healthz">Server health</a></p>
</main>
</body>
</html>`
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(body))
	}
}

// staticHandler serves the embedded /static/ tree with long-cache headers.
// Per PLAN.md §6h: static assets get long-cache because they're versioned
// either by content-addressed asset paths (CDN) or by build-time hashes.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticfs.FS, ".")
	if err != nil {
		// If this fails the binary is misbuilt; fail loudly at boot.
		panic("static: " + err.Error())
	}
	fileSrv := http.FileServer(http.FS(sub))
	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileSrv.ServeHTTP(w, r)
	}))
}

type healthResponse struct {
	OK       bool              `json:"ok"`
	Services map[string]string `json:"services"`
}

func healthzHandler(h Health) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		out := healthResponse{OK: true, Services: map[string]string{}}

		check := func(name string, p Pinger) {
			if p == nil {
				out.Services[name] = "skipped"
				return
			}
			if err := p.Ping(ctx); err != nil {
				out.OK = false
				out.Services[name] = "down: " + err.Error()
				slog.Warn("healthz dependency down", "service", name, "err", err)
			} else {
				out.Services[name] = "ok"
			}
		}
		check("postgres", h.Postgres)
		check("redis", h.Redis)

		status := http.StatusOK
		if !out.OK {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(out)
	}
}
