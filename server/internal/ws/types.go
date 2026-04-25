// Package ws is the WebSocket gateway. Single endpoint /ws upgrades any
// connection; the first FlatBuffers message must be Auth, which tags the
// connection with realm = player|designer. All subsequent messages are
// ClientMessage envelopes (verb + payload bytes), dispatched by verb.
//
// PLAN.md §1, §4l: realm tagging is enforced at dispatch time; opcode
// handlers check connection.realm, never the token. Sandbox WS instances
// live under sandbox:<designer_id>:<map_id> in a separate id space.
//
// Per-connection state (subscriber id, AOI subscription, rate limiter,
// reconnect token) lives on Connection. The gateway owns a registry of
// live connections so the broadcaster can iterate them per tick.
package ws

import (
	"sync/atomic"

	"github.com/coder/websocket"

	"boxland/server/internal/sim/aoi"
)

// Realm identifies the auth realm the connection was opened under.
// Mirrors input.fbs Realm enum.
type Realm uint8

const (
	RealmInvalid Realm = iota
	RealmPlayer
	RealmDesigner
)

// String renders the realm for log diagnostics.
func (r Realm) String() string {
	switch r {
	case RealmPlayer:
		return "player"
	case RealmDesigner:
		return "designer"
	}
	return "invalid"
}

// ClientKind mirrors input.fbs ClientKind.
type ClientKind uint8

const (
	ClientUnknown ClientKind = iota
	ClientWeb
	ClientIOS // reserved for v1.1
)

// SubjectID identifies the authenticated subject:
//   * Realm == player    -> player_id
//   * Realm == designer  -> designer_id
type SubjectID int64

// Connection is one live WebSocket. The gateway owns its lifecycle; the
// dispatcher mutates per-conn state via the supplied pointer.
type Connection struct {
	ws         *websocket.Conn
	id         ConnID
	realm      Realm
	subject    SubjectID
	clientKind ClientKind
	clientVer  string

	// Subscription is the AOI bookkeeping; nil until the connection
	// has joined a map (JoinMap verb).
	Subscription *aoi.Subscription

	// closed is set atomically when the connection is being torn down so
	// concurrent readers + writers see a single source of truth.
	closed atomic.Bool
}

// ConnID is the per-process unique handle for a connection. Wraps a
// uint64 so the gateway can compare/store without exposing pointer
// identity to callers.
type ConnID uint64

// ID returns the connection's stable id.
func (c *Connection) ID() ConnID { return c.id }

// Realm returns the connection's auth realm. Set once at handshake time;
// never mutates.
func (c *Connection) Realm() Realm { return c.realm }

// Subject returns the authenticated player or designer id (depending on
// realm). Set once at handshake time.
func (c *Connection) Subject() SubjectID { return c.subject }

// ClientKind returns whatever the Auth message claimed.
func (c *Connection) ClientKind() ClientKind { return c.clientKind }

// ClientVersion returns whatever the Auth message claimed.
func (c *Connection) ClientVersion() string { return c.clientVer }

// IsClosed reports whether the connection has been torn down.
func (c *Connection) IsClosed() bool { return c.closed.Load() }
