// Package aoi implements per-subscriber Area-of-Interest bookkeeping for
// the WebSocket broadcaster (PLAN.md §1, §4h, §4l).
//
// Each subscriber owns a per-chunk version vector. On every tick the
// broadcaster:
//   1. Computes the subscriber's currently-relevant chunks (cells around
//      its focus, e.g. the player's position).
//   2. Compares each chunk's grid.Version against the version the
//      subscriber last acked.
//   3. Sends a Diff for only the chunks whose version advanced.
//
// This avoids the "per-player snapshot copy" anti-pattern from the
// architecture refinement discussion (issue #5 in the resolutions).
package aoi

import (
	"boxland/server/internal/sim/spatial"
)

// SubscriberID identifies one subscribed WebSocket connection.
type SubscriberID uint64

// Policy governs the subscriber's chunk set + field set. Mirrors PLAN.md
// §4l "one broadcaster, three subscriber policies" (Player, Designer,
// Spectator).
type Policy uint8

const (
	PolicyPlayer Policy = iota
	PolicyDesigner
	PolicySpectator
)

// Subscription is the per-connection AOI state.
type Subscription struct {
	ID     SubscriberID
	Policy Policy

	// FocusChunk is the centre of the subscriber's AOI window. For
	// players this tracks the player entity's chunk; for spectators it
	// tracks the camera; for designers it's whatever they're looking at.
	FocusChunk spatial.ChunkID

	// RadiusChunks is the half-extent of the AOI window in chunks.
	// 1 = 3x3 (focus + 1 ring); 2 = 5x5 (focus + 2 rings); etc.
	// Designers can override to scan the whole map.
	RadiusChunks int32

	// Acked tracks the last grid Version the subscriber confirmed for
	// each chunk. Chunks the subscriber has never seen are absent from
	// the map; the broadcaster treats absent as "version 0", forcing a
	// full chunk send the first time it enters AOI.
	Acked map[spatial.ChunkID]uint64

	// FollowTarget is the player_id this subscription tracks every
	// tick. Set by the Spectate verb when Mode == FollowPlayer; the
	// per-tick re-centre happens upstream of the broadcaster (the
	// runtime looks up the target's chunk and updates FocusChunk).
	// Zero means "no follow"; that includes both player-realm
	// subscribers (their Subscription is centred on their own entity
	// via a different code path) and FreeCam spectators.
	FollowTarget uint64

	// FreeCam disables the FollowTarget recentre even if FollowTarget
	// is set. Mostly informational so the runtime doesn't need to
	// special-case "FollowTarget == 0 vs FreeCam == true". Spectate
	// hands both fields off explicitly.
	FreeCam bool
}

// NewSubscription returns a fresh subscription with an empty ack map.
func NewSubscription(id SubscriberID, policy Policy, focus spatial.ChunkID, radius int32) *Subscription {
	if radius < 0 {
		radius = 0
	}
	return &Subscription{
		ID:           id,
		Policy:       policy,
		FocusChunk:   focus,
		RadiusChunks: radius,
		Acked:        make(map[spatial.ChunkID]uint64),
	}
}

// VisibleChunks returns the AOI window — every ChunkID inside the
// subscriber's radius, including the focus chunk itself. Cheap; no
// allocation if RadiusChunks is small.
func (s *Subscription) VisibleChunks() []spatial.ChunkID {
	cx, cy := s.FocusChunk.Coords()
	r := s.RadiusChunks
	side := 2*r + 1
	out := make([]spatial.ChunkID, 0, side*side)
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			out = append(out, spatial.MakeChunkID(cx+dx, cy+dy))
		}
	}
	return out
}

// DirtyChunks returns the subset of VisibleChunks whose current Grid
// Version exceeds the subscriber's Acked version. Side effect: for any
// chunk the subscriber has never seen (ack absent), it's treated as
// version 0 — so a fresh subscriber receives every visible chunk on the
// first call.
func (s *Subscription) DirtyChunks(g *spatial.Grid) []spatial.ChunkID {
	visible := s.VisibleChunks()
	out := make([]spatial.ChunkID, 0, len(visible))
	for _, c := range visible {
		v := g.Version(c)
		if v > s.Acked[c] {
			out = append(out, c)
		}
	}
	return out
}

// Ack records that the subscriber has received the chunk's state at the
// given version. Subsequent DirtyChunks calls won't include this chunk
// until its grid Version advances.
func (s *Subscription) Ack(c spatial.ChunkID, v uint64) {
	s.Acked[c] = v
}

// AckAll records the current grid Version of every visible chunk.
// Convenience wrapper for the common "broadcaster sent everything dirty;
// mark them all acked" pattern at end of tick.
func (s *Subscription) AckAll(g *spatial.Grid) {
	for _, c := range s.VisibleChunks() {
		s.Acked[c] = g.Version(c)
	}
}

// SetFocus moves the AOI window. Chunks now outside the visible window
// remain in Acked (cheap; trim periodically if memory pressure ever
// matters), but they don't show up in DirtyChunks until they re-enter.
func (s *Subscription) SetFocus(c spatial.ChunkID) {
	s.FocusChunk = c
}

// ForgetChunk drops the ack for a single chunk. Used when the broadcaster
// detects a backpressure condition for a specific chunk and wants to
// re-deliver it from scratch.
func (s *Subscription) ForgetChunk(c spatial.ChunkID) {
	delete(s.Acked, c)
}

// Reset clears all acks so the next DirtyChunks call returns every visible
// chunk. Used by the reconnect path (PLAN.md §4l) when the gap exceeds
// the resend threshold.
func (s *Subscription) Reset() {
	s.Acked = make(map[spatial.ChunkID]uint64)
}
