// Package smoke is the end-to-end integration test that walks every
// major surface in one go: designer signup -> entity type -> map ->
// tile placement -> player signup -> WS JoinMap -> chunk-loaded tiles.
//
// PLAN.md §138. Designed to run inside `just test` against the dev
// docker-compose stack; CI gates its inclusion behind TEST_DATABASE_URL
// + TEST_REDIS_URL the same way every other integration test does.
//
// What's covered today:
//   * Designer create + entity type create
//   * Map create + layer + tile placement via the maps service
//   * Player create + WS handshake + JoinMap that lands a Subscription
//   * Chunk loaded into the runtime contains the placed tile
//
// Deferred to a follow-up: asset upload (needs MinIO in CI),
// publish-pipeline-driven hot-swap broadcast (needs the runtime
// spawn-on-JoinMap path that v1.x runtime doesn't yet implement).
package smoke_test

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"

	authdesigner "boxland/server/internal/auth/designer"
	authplayer "boxland/server/internal/auth/player"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/proto"
	"boxland/server/internal/sim/runtime"
	"boxland/server/internal/sim/spatial"
	"boxland/server/internal/ws"

	flatbuffers "github.com/google/flatbuffers/go"
)

// openPool returns an isolated, freshly-migrated DB. testdb.New wires its own t.Cleanup that drops the database when the test ends.
func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

func openRedis(t *testing.T) rueidis.Client {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6380/0"
	}
	opts, _ := rueidis.ParseURL(url)
	cli, err := rueidis.NewClient(opts)
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	if err := cli.Do(context.Background(), cli.B().Ping().Build()).Error(); err != nil {
		cli.Close()
		t.Skipf("redis unavailable: %v", err)
	}
	return cli
}

func encodeAuth(realm proto.Realm, token string) []byte {
	b := flatbuffers.NewBuilder(64)
	tokOff := b.CreateString(token)
	verOff := b.CreateString("smoke-test")
	proto.ProtocolVersionStart(b)
	proto.ProtocolVersionAddMajor(b, 1)
	proto.ProtocolVersionAddMinor(b, 0)
	pvOff := proto.ProtocolVersionEnd(b)
	proto.AuthStart(b)
	proto.AuthAddProtocolVersion(b, pvOff)
	proto.AuthAddRealm(b, realm)
	proto.AuthAddToken(b, tokOff)
	proto.AuthAddClientKind(b, proto.ClientKindWeb)
	proto.AuthAddClientVersion(b, verOff)
	root := proto.AuthEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}

func encodeJoinMap(mapID uint32) []byte {
	b := flatbuffers.NewBuilder(32)
	hint := b.CreateString("")
	proto.JoinMapPayloadStart(b)
	proto.JoinMapPayloadAddMapId(b, mapID)
	proto.JoinMapPayloadAddInstanceHint(b, hint)
	root := proto.JoinMapPayloadEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}

func encodeClientMessage(verb proto.Verb, payload []byte) []byte {
	b := flatbuffers.NewBuilder(64)
	var offset flatbuffers.UOffsetT
	if len(payload) > 0 {
		proto.ClientMessageStartPayloadVector(b, len(payload))
		for i := len(payload) - 1; i >= 0; i-- {
			b.PrependByte(payload[i])
		}
		offset = b.EndVector(len(payload))
	}
	proto.ClientMessageStart(b)
	proto.ClientMessageAddVerb(b, verb)
	if offset != 0 {
		proto.ClientMessageAddPayload(b, offset)
	}
	root := proto.ClientMessageEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}

func TestSmoke_DesignerToPlayerEndToEnd(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	cli := openRedis(t)
	defer cli.Close()

	ctx := context.Background()

	// --- 1. Designer signup ---
	authD := authdesigner.New(pool)
	d, err := authD.CreateDesigner(ctx, "smoke-designer@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}

	// --- 2. Entity type ---
	ents := entities.New(pool, components.Default())
	et, err := ents.Create(ctx, entities.CreateInput{
		Name: "smoke-wall", CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatalf("create entity type: %v", err)
	}

	// --- 3. Map ---
	mapsSvc := maps.New(pool)
	m, err := mapsSvc.Create(ctx, maps.CreateInput{
		Name: "smoke-map", Width: 64, Height: 64, Public: true, CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	layers, err := mapsSvc.Layers(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	var baseLayerID int64
	for _, l := range layers {
		if l.Kind == "tile" {
			baseLayerID = l.ID
			break
		}
	}
	if baseLayerID == 0 {
		t.Fatal("no tile layer on smoke-map")
	}

	// --- 4. Place a tile via the service. The WS path is exercised
	//     by authoring_test.go separately; the smoke test asserts the
	//     end-to-end stack works after the WS-side has already
	//     persisted. ---
	if err := mapsSvc.PlaceTiles(ctx, []maps.Tile{{
		MapID: m.ID, LayerID: baseLayerID, X: 5, Y: 5, EntityTypeID: et.ID,
	}}); err != nil {
		t.Fatalf("place tile: %v", err)
	}

	// --- 5. Player signup + access JWT ---
	authP := authplayer.New(pool, []byte("smoke-jwt-secret-32-bytes-padded__"))
	p, err := authP.CreatePlayer(ctx, "smoke-player@x.com", "playerpass")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	jwt, err := authP.MintAccessToken(p)
	if err != nil {
		t.Fatal(err)
	}

	// --- 6. WS gateway: player handshake + JoinMap ---
	mgr := runtime.NewInstanceManager(pool, cli, mapsSvc)
	dispatcher := ws.NewDispatcher()
	ws.RegisterDefaultVerbs(dispatcher)
	ws.RegisterAuthoringVerbs(dispatcher, ws.AuthoringDeps{
		MapsService: mapsSvc, Instances: mgr,
	})
	authBackend := &ws.LiveAuthBackend{Player: authP, Designer: authD}
	g := ws.NewGateway(authBackend, dispatcher, ws.Options{})
	srv := httptest.NewServer(g.HTTPHandler())
	defer srv.Close()

	dialCtx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	c, _, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	if err := c.Write(dialCtx, websocket.MessageBinary, encodeAuth(proto.RealmPlayer, jwt)); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(dialCtx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbJoinMap, encodeJoinMap(uint32(m.ID)))); err != nil {
		t.Fatal(err)
	}

	// Wait for the gateway to register the conn + dispatcher to apply
	// the JoinMap (subscription attached).
	deadline := time.Now().Add(2 * time.Second)
	var conn *ws.Connection
	for time.Now().Before(deadline) {
		conns := g.Conns()
		if len(conns) >= 1 && conns[0].Subscription != nil {
			conn = conns[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		t.Fatal("WS conn or subscription never attached after JoinMap")
	}

	// --- 7. Verify the runtime is wired: chunk load against the
	//     instance materializes the placed tile ---
	mi := mgr.Get("live:" + itoa(m.ID) + ":0")
	if mi == nil {
		t.Fatal("instance manager has no live instance after JoinMap")
	}

	// Force-load the chunk that holds (5,5). The tile lives at gx=5
	// gy=5 -> chunkID (0,0) under the 16-tile chunk size. The runtime
	// injects an EntityTypeLookup adapter; for the smoke test we use
	// the entities.Service directly via a tiny inline adapter.
	lookup := smokeLookup{ents: ents}
	res, err := mi.LoadChunk(ctx, lookup, spatial.MakeChunkID(0, 0))
	if err != nil {
		t.Fatalf("load chunk: %v", err)
	}
	if res.TilesSpawned == 0 {
		t.Fatalf("chunk loaded but spawned no tiles; expected >= 1 (the smoke wall)")
	}

	// --- 8. Sanity: chunk version vector advanced for the AOI
	//     subscription, so a broadcaster tick would emit a Diff. ---
	v := mi.Grid.Version(spatial.MakeChunkID(0, 0))
	if v == 0 {
		t.Errorf("chunk version did not advance after LoadChunk")
	}
}

// smokeLookup is a local EntityTypeLookup adapter that delegates to
// entities.Service. Real production wiring lives in the runtime
// package; the smoke test inlines it to avoid importing the full
// runtime adapter graph.
type smokeLookup struct {
	ents *entities.Service
}

func (s smokeLookup) EntityTypeMeta(ctx context.Context, id int64) (*maps.EntityTypeMeta, error) {
	et, err := s.ents.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &maps.EntityTypeMeta{
		ID:                   et.ID,
		SpriteAssetID:        nil,
		DefaultAnimationID:   nil,
		ColliderW:            32,
		ColliderH:            32,
		DefaultCollisionMask: 1,
	}, nil
}

// itoa is local-package; avoids pulling in strconv just for the tiny use.
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
