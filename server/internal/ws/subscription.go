package ws

import (
	"sync/atomic"

	"boxland/server/internal/sim/aoi"
	"boxland/server/internal/sim/spatial"
)

// AOI default radius (chunks). Each chunk is 16 tiles = 512 px.
// 4 -> 9x9 chunks visible = 144x144 tiles around the focus, comfortable
// at typical aspect ratios. Designers in Mapmaker get a wider radius so
// large strokes are visible immediately.
const (
	playerAOIRadiusChunks    int32 = 4
	designerAOIRadiusChunks  int32 = 8
)

// subscriptionIDSeq mints a fresh aoi.SubscriberID per connection.
var subscriptionIDSeq atomic.Uint64

// newSubscriptionForConn builds an aoi.Subscription scoped to the
// connection. The id is a per-process monotonically increasing counter
// (NOT the connection's ConnID -- separate type, separate space).
func newSubscriptionForConn(conn *Connection, policy aoi.Policy, focus spatial.ChunkID) *aoi.Subscription {
	id := aoi.SubscriberID(subscriptionIDSeq.Add(1))
	radius := playerAOIRadiusChunks
	if conn.Realm() == RealmDesigner {
		radius = designerAOIRadiusChunks
	}
	return aoi.NewSubscription(id, policy, focus, radius)
}

// defaultPolicyForRealm picks the aoi.Policy each realm gets by default.
// Spectator policy is only set explicitly by the Spectate verb handler.
func defaultPolicyForRealm(r Realm) aoi.Policy {
	if r == RealmDesigner {
		return aoi.PolicyDesigner
	}
	return aoi.PolicyPlayer
}
