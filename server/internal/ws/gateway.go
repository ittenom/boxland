package ws

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"boxland/server/internal/proto"
)

// writeTimeout caps how long we wait on a single broadcaster write before
// declaring the connection unhealthy and closing it. 5s is generous for
// small per-tick Diff blobs; misbehaving clients trip this fast.
const writeTimeout = 5 * time.Second

// Gateway owns the upgrade endpoint + the live-connection registry.
// One gateway per server process; share it across maps via the broadcaster.
type Gateway struct {
	auth        AuthBackend
	dispatcher  *Dispatcher

	// Per-connection registry. Access is mu-protected because both the
	// upgrade handler and the broadcaster goroutine mutate it.
	mu     sync.RWMutex
	conns  map[ConnID]*Connection
	nextID atomic.Uint64

	// Defaults applied to every new connection's rate limiter.
	burst  int
	perSec int
}

// Options tune Gateway construction. All fields are optional; sensible
// defaults are documented inline.
type Options struct {
	RateBurst     int // default 100
	RatePerSecond int // default 50
}

// NewGateway returns a fresh Gateway bound to the given auth backend and
// dispatcher. The dispatcher should be fully populated before the first
// upgrade is accepted; registering verbs after Serve is racy.
func NewGateway(auth AuthBackend, dispatcher *Dispatcher, opts Options) *Gateway {
	g := &Gateway{
		auth:       auth,
		dispatcher: dispatcher,
		conns:      make(map[ConnID]*Connection),
		burst:      opts.RateBurst,
		perSec:     opts.RatePerSecond,
	}
	if g.burst <= 0 {
		g.burst = 100
	}
	if g.perSec <= 0 {
		g.perSec = 50
	}
	return g
}

// HTTPHandler returns the http.Handler that performs the WS upgrade.
// Mount it at /ws.
func (g *Gateway) HTTPHandler() http.Handler {
	return http.HandlerFunc(g.serveUpgrade)
}

// Conns returns a snapshot of every live connection. The returned slice
// is owned by the caller; safe to iterate while connections close.
func (g *Gateway) Conns() []*Connection {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*Connection, 0, len(g.conns))
	for _, c := range g.conns {
		out = append(out, c)
	}
	return out
}

// CloseAll terminates every live connection. Used during graceful
// shutdown so backlogged messages don't block the http.Server.Shutdown
// path.
func (g *Gateway) CloseAll(reason string) {
	g.mu.Lock()
	conns := make([]*Connection, 0, len(g.conns))
	for _, c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	for _, c := range conns {
		_ = c.ws.Close(websocket.StatusGoingAway, reason)
	}
}

// ---- internals ----

func (g *Gateway) serveUpgrade(w http.ResponseWriter, r *http.Request) {
	// CompressionMode: disable to keep the FlatBuffers payload byte-for-
	// byte stable across the wire (compressed framing complicates wire
	// debugging without much win for small per-tick diffs).
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
		// We expect specific subprotocols if a client opts in, but for
		// v1 anything goes; the protocol is FlatBuffers-binary either way.
	})
	if err != nil {
		// Accept already wrote the 4xx; no further response.
		slog.Warn("ws upgrade failed", "err", err, "remote", r.RemoteAddr)
		return
	}

	id := ConnID(g.nextID.Add(1))
	conn := &Connection{
		ws: wsConn,
		id: id,
	}

	peerIP := clientIP(r)
	ctx := r.Context()

	// Auth handshake. On failure we close immediately with a policy code.
	if err := g.performAuthHandshake(ctx, conn, peerIP); err != nil {
		slog.Info("ws auth failed", "err", err, "remote", r.RemoteAddr, "conn", id)
		_ = wsConn.Close(websocket.StatusPolicyViolation, "auth failed")
		return
	}

	g.mu.Lock()
	g.conns[id] = conn
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.conns, id)
		g.mu.Unlock()
		conn.closed.Store(true)
	}()

	slog.Info("ws connected",
		"conn", id, "realm", conn.Realm(), "subject", conn.Subject(),
		"client_kind", conn.ClientKind(), "client_version", conn.ClientVersion(),
	)

	g.readLoop(ctx, conn, peerIP)
}

// readLoop is the per-connection message pump. Runs until the client
// closes, the connection errors, or rate-limit/abuse triggers a close.
func (g *Gateway) readLoop(parent context.Context, conn *Connection, peerIP net.IP) {
	limiter := NewRateLimiter(g.burst, g.perSec)
	abuseStrikes := 0

	for {
		// Per-message timeout: a quiet connection should still send Heartbeat
		// or move once every minute. 90s gives us ample slack.
		readCtx, cancel := context.WithTimeout(parent, 90*time.Second)
		_, blob, err := conn.ws.Read(readCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.Canceled) || isCloseError(err) {
				return
			}
			slog.Info("ws read closed", "conn", conn.ID(), "err", err)
			_ = conn.ws.Close(websocket.StatusNormalClosure, "")
			return
		}

		if !limiter.Allow() {
			abuseStrikes++
			slog.Warn("ws rate limited",
				"conn", conn.ID(), "subject", conn.Subject(),
				"realm", conn.Realm(), "strikes", abuseStrikes, "peer_ip", peerIP,
			)
			if abuseStrikes >= 5 {
				_ = conn.ws.Close(websocket.StatusPolicyViolation, "rate limit exceeded")
				return
			}
			continue
		}
		abuseStrikes = 0

		if len(blob) < 8 {
			slog.Info("ws short message", "conn", conn.ID(), "len", len(blob))
			continue
		}
		msg := proto.GetRootAsClientMessage(blob, 0)
		if err := g.dispatcher.Dispatch(parent, conn, msg); err != nil {
			slog.Warn("ws dispatch error", "conn", conn.ID(), "err", err, "verb", msg.Verb())
			// Real errors close the connection; recoverable ones (like
			// "verb not yet handled") are logged and the loop continues.
			// For v1 the dispatcher returns an error in both cases; we
			// only close for realm-violation errors.
			if isRealmViolation(err) {
				_ = conn.ws.Close(websocket.StatusPolicyViolation, "realm violation")
				return
			}
		}
	}
}

func isCloseError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "EOF") || strings.Contains(s, "closed")
}

func isRealmViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// Match the canonical realm-error strings from spectate.go,
	// authoring.go (sandbox JoinMap), sandbox_ops.go (designer opcodes),
	// and dispatcher.go (designer-only verb on player realm).
	return strings.Contains(s, "requires designer realm") ||
		strings.Contains(s, "sandbox") ||
		strings.Contains(s, "spectate: ") ||
		strings.Contains(s, "designer_command: opcode requires")
}

// clientIP returns the most likely client IP. Trusts X-Forwarded-For only
// when the immediate peer is loopback (matches the helper in the
// designer HTTP package).
func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer != nil && peer.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
			if ip := net.ParseIP(first); ip != nil {
				return ip
			}
		}
	}
	return peer
}
