package ws

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/coder/websocket"

	"boxland/server/internal/sim/aoi"
)

// Policy governs how the broadcaster computes the chunk + field set for
// one connection's per-tick Diff. Mirrors aoi.Policy and PLAN.md §1
// "Broadcast model".
//
// PolicyPlayer:    bounded AOI radius, minimal fields.
// PolicyDesigner:  unbounded radius, inspector fields included.
// PolicySpectator: bounded AOI radius, minimal fields, no own-entity.
type BroadcastPolicy uint8

const (
	BroadcastPlayer BroadcastPolicy = iota
	BroadcastDesigner
	BroadcastSpectator
)

// Broadcaster pushes per-tick Diff blobs to subscribed connections. One
// broadcaster per Gateway; the per-tick scheduler calls Tick.
//
// The per-connection AOI subscription lives on Connection.Subscription
// (set by the JoinMap handler); the broadcaster reads + advances it.
type Broadcaster struct {
	gateway *Gateway

	// Encoder builds the actual Diff blob a subscriber should receive,
	// given the world snapshot and the subscription's dirty chunks.
	// Production wiring injects an encoder that reads from the live
	// World; tests inject a stub that returns canned bytes.
	encoder DiffEncoder

	mu       sync.RWMutex
	policies map[ConnID]BroadcastPolicy
}

// DiffEncoder is the per-connection blob builder. The broadcaster calls
// this for each connection that has at least one dirty chunk; the
// encoder returns the FlatBuffers Diff blob to send.
//
// Returning (nil, nil) means "nothing to send this tick"; the
// broadcaster skips writing.
type DiffEncoder func(conn *Connection, policy BroadcastPolicy) ([]byte, error)

// NewBroadcaster wires the broadcaster onto a Gateway.
func NewBroadcaster(g *Gateway, enc DiffEncoder) *Broadcaster {
	return &Broadcaster{
		gateway:  g,
		encoder:  enc,
		policies: make(map[ConnID]BroadcastPolicy),
	}
}

// SetPolicy assigns the policy for a connection. The default if unset is
// PolicyPlayer for player-realm conns and PolicyDesigner for designer
// conns; SetPolicy lets the caller explicitly mark a designer as a
// spectator (e.g. for spectator-mode of a live game).
func (b *Broadcaster) SetPolicy(id ConnID, p BroadcastPolicy) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.policies[id] = p
}

// PolicyFor returns the policy currently assigned to a connection. Order
// of precedence:
//
//  1. Explicit override set via SetPolicy (rarely used; tests + ad-hoc).
//  2. The connection's AOI Subscription.Policy (set by JoinMap or
//     Spectate at the moment of subscription).
//  3. The realm default — Designer realm gets BroadcastDesigner, anything
//     else gets BroadcastPlayer.
//
// Keeping the "real" choice on the Subscription means the spectate
// handler doesn't need to reach into broadcaster state to flag the
// connection — setting `subscription.Policy = aoi.PolicySpectator` is
// enough for the broadcaster to apply the spectator field set on the
// next tick.
func (b *Broadcaster) PolicyFor(conn *Connection) BroadcastPolicy {
	b.mu.RLock()
	if p, ok := b.policies[conn.ID()]; ok {
		b.mu.RUnlock()
		return p
	}
	b.mu.RUnlock()
	if conn.Subscription != nil {
		switch conn.Subscription.Policy {
		case aoi.PolicySpectator:
			return BroadcastSpectator
		case aoi.PolicyDesigner:
			return BroadcastDesigner
		case aoi.PolicyPlayer:
			return BroadcastPlayer
		}
	}
	if conn.Realm() == RealmDesigner {
		return BroadcastDesigner
	}
	return BroadcastPlayer
}

// Tick walks every connection, runs the encoder, and writes the resulting
// blob. Per-connection write errors close the connection.
//
// Called from the system scheduler after the broadcast stage.
func (b *Broadcaster) Tick(ctx context.Context) {
	conns := b.gateway.Conns()
	for _, conn := range conns {
		if conn.IsClosed() {
			continue
		}
		if conn.Subscription == nil {
			continue // hasn't joined a map yet
		}
		policy := b.PolicyFor(conn)
		blob, err := b.encoder(conn, policy)
		if err != nil {
			slog.Warn("broadcaster encode", "conn", conn.ID(), "err", err)
			continue
		}
		if blob == nil {
			continue
		}
		writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
		err = conn.ws.Write(writeCtx, websocket.MessageBinary, blob)
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Info("broadcaster write closed conn", "conn", conn.ID(), "err", err)
			}
			_ = conn.ws.Close(websocket.StatusNormalClosure, "broadcaster write failed")
		}
	}
}

// Forget releases per-connection broadcaster state. Called by the gateway
// when a connection closes; safe to call against unknown ids.
func (b *Broadcaster) Forget(id ConnID) {
	b.mu.Lock()
	delete(b.policies, id)
	b.mu.Unlock()
}
