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
}

// New builds the http.Handler tree. Add routes here as packages come online.
func New(h Health, m Mounts) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(h))
	mux.Handle("GET /static/", staticHandler())
	if m.Designer != nil {
		mux.Handle("/design/", m.Designer)
	}
	return mux
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
