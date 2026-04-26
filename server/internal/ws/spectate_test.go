package ws_test

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	flatbuffers "github.com/google/flatbuffers/go"

	authdesigner "boxland/server/internal/auth/designer"
	authplayer "boxland/server/internal/auth/player"
	"boxland/server/internal/maps"
	"boxland/server/internal/proto"
	"boxland/server/internal/sim/aoi"
	"boxland/server/internal/sim/runtime"
	boxws "boxland/server/internal/ws"
)

// encodeSpectate builds a SpectatePayload for the given map + mode. Used
// by the integration tests below to script the second-frame payload after
// the Auth handshake.
func encodeSpectate(mapID uint32, hint string, mode proto.SpectateMode, target uint64) []byte {
	b := flatbuffers.NewBuilder(64)
	hintOffset := b.CreateString(hint)
	proto.SpectatePayloadStart(b)
	proto.SpectatePayloadAddMapId(b, mapID)
	proto.SpectatePayloadAddInstanceHint(b, hintOffset)
	proto.SpectatePayloadAddMode(b, mode)
	proto.SpectatePayloadAddTargetPlayerId(b, target)
	root := proto.SpectatePayloadEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}

// spectateFixture stands up the Postgres-backed maps + auth services,
// plus a WS gateway with the spectator + authoring verbs registered.
// Returns enough handles for the per-test setup to create maps with
// specific spectator_policy values + dial player or designer realms.
type spectateFixture struct {
	t           *testing.T
	server      *httptest.Server
	mapsSvc     *maps.Service
	authDesigner *authdesigner.Service
	authPlayer  *authplayer.Service
	gateway     *boxws.Gateway
	broadcaster *boxws.Broadcaster
	designerID  int64
	playerID    int64
	playerJWT   string
	designerTok string
}

func newSpectateFixture(t *testing.T) *spectateFixture {
	t.Helper()
	pool := openPool(t)
	t.Cleanup(pool.Close)
	cli := openRedis(t)
	t.Cleanup(cli.Close)

	authD := authdesigner.New(pool)
	d, err := authD.CreateDesigner(context.Background(), "spectate-d@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}

	authP := authplayer.New(pool, []byte("test-jwt-secret-32-bytes-padded__"))
	pl, err := authP.CreatePlayer(context.Background(), "spectate-p@x.com", "playerpass")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	jwt, err := authP.MintAccessToken(pl)
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}

	mapsSvc := maps.New(pool)

	mgr := runtime.NewInstanceManager(pool, cli, mapsSvc)
	dispatcher := boxws.NewDispatcher()
	boxws.RegisterDefaultVerbs(dispatcher)
	deps := boxws.AuthoringDeps{MapsService: mapsSvc, Instances: mgr}
	boxws.RegisterAuthoringVerbs(dispatcher, deps)
	boxws.RegisterSpectatorVerb(dispatcher, deps)

	authBackend := &boxws.LiveAuthBackend{Player: authP, Designer: authD}
	g := boxws.NewGateway(authBackend, dispatcher, boxws.Options{})
	srv := httptest.NewServer(g.HTTPHandler())
	t.Cleanup(srv.Close)

	br := boxws.NewBroadcaster(g, func(*boxws.Connection, boxws.BroadcastPolicy) ([]byte, error) {
		return nil, nil
	})

	designerTicket, err := authD.MintWSTicket(context.Background(), d.ID, net.ParseIP("127.0.0.1"))
	if err != nil {
		t.Fatalf("mint ticket: %v", err)
	}

	return &spectateFixture{
		t:            t,
		server:       srv,
		mapsSvc:      mapsSvc,
		authDesigner: authD,
		authPlayer:   authP,
		gateway:      g,
		broadcaster:  br,
		designerID:   d.ID,
		playerID:     pl.ID,
		playerJWT:    jwt,
		designerTok:  designerTicket,
	}
}

func (f *spectateFixture) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

// awaitConn waits until the gateway registers exactly one connection,
// then returns it. Tests use this to grab a *Connection so they can
// inspect Subscription state set by handlers.
func (f *spectateFixture) awaitConn(t *testing.T) *boxws.Connection {
	t.Helper()
	for i := 0; i < 100; i++ {
		conns := f.gateway.Conns()
		if len(conns) >= 1 {
			return conns[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no connection registered within timeout")
	return nil
}

func (f *spectateFixture) createMap(t *testing.T, name, policy string, public bool) *maps.Map {
	t.Helper()
	m, err := f.mapsSvc.Create(context.Background(), maps.CreateInput{
		Name:            name,
		Width:           64,
		Height:          64,
		Public:          public,
		SpectatorPolicy: policy,
		CreatedBy:       f.designerID,
	})
	if err != nil {
		t.Fatalf("create map %q: %v", name, err)
	}
	return m
}

// ---- public-policy: any player allowed ----

func TestSpectate_PublicMap_PlayerAllowed_AndPolicyIsSpectator(t *testing.T) {
	f := newSpectateFixture(t)
	m := f.createMap(t, "pub-map", "public", true)

	c := f.dial(t)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, f.playerJWT, proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbSpectate, encodeSpectate(uint32(m.ID), "", proto.SpectateModeFreeCam, 0))); err != nil {
		t.Fatal(err)
	}

	conn := f.awaitConn(t)

	// Wait for the handler to attach a Subscription.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn.Subscription != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn.Subscription == nil {
		t.Fatal("Subscription not attached after spectate")
	}
	if conn.Subscription.Policy != aoi.PolicySpectator {
		t.Errorf("Subscription.Policy: got %v, want PolicySpectator", conn.Subscription.Policy)
	}
	if !conn.Subscription.FreeCam {
		t.Errorf("Subscription.FreeCam: got false, want true (FreeCam mode)")
	}
	if got := f.broadcaster.PolicyFor(conn); got != boxws.BroadcastSpectator {
		t.Errorf("Broadcaster.PolicyFor: got %v, want BroadcastSpectator", got)
	}
}

// ---- private-policy: player rejected, designer allowed ----

func TestSpectate_PrivateMap_PlayerRejected(t *testing.T) {
	f := newSpectateFixture(t)
	m := f.createMap(t, "priv-map", "private", false)

	c := f.dial(t)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, f.playerJWT, proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbSpectate, encodeSpectate(uint32(m.ID), "", proto.SpectateModeFreeCam, 0))); err != nil {
		t.Fatal(err)
	}

	conn := f.awaitConn(t)

	// Give the dispatcher a chance to run + reject.
	time.Sleep(150 * time.Millisecond)

	// The handler should NOT have attached a Subscription.
	if conn.Subscription != nil {
		t.Errorf("private-map spectate should be rejected; Subscription was attached")
	}
}

func TestSpectate_PrivateMap_DesignerAllowed(t *testing.T) {
	f := newSpectateFixture(t)
	m := f.createMap(t, "priv-map", "private", false)

	c := f.dial(t)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmDesigner, f.designerTok, proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbSpectate, encodeSpectate(uint32(m.ID), "", proto.SpectateModeFollowPlayer, 999))); err != nil {
		t.Fatal(err)
	}

	conn := f.awaitConn(t)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn.Subscription != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn.Subscription == nil {
		t.Fatal("designer spectator should be allowed on private maps")
	}
	if conn.Subscription.Policy != aoi.PolicySpectator {
		t.Errorf("Subscription.Policy: got %v, want PolicySpectator", conn.Subscription.Policy)
	}
	if conn.Subscription.FollowTarget != 999 {
		t.Errorf("Subscription.FollowTarget: got %d, want 999", conn.Subscription.FollowTarget)
	}
	if conn.Subscription.FreeCam {
		t.Errorf("Subscription.FreeCam: got true, want false (FollowPlayer mode)")
	}
}

// ---- invite-policy: player rejected without invite, allowed with one ----

func TestSpectate_InviteMap_PlayerRejectedWithoutInvite(t *testing.T) {
	f := newSpectateFixture(t)
	m := f.createMap(t, "inv-map", "invite", false)

	c := f.dial(t)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, f.playerJWT, proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbSpectate, encodeSpectate(uint32(m.ID), "", proto.SpectateModeFreeCam, 0))); err != nil {
		t.Fatal(err)
	}

	conn := f.awaitConn(t)
	time.Sleep(150 * time.Millisecond)
	if conn.Subscription != nil {
		t.Errorf("invite-map spectate without invite should be rejected; Subscription was attached")
	}
}

func TestSpectate_InviteMap_PlayerAllowedWithInvite(t *testing.T) {
	f := newSpectateFixture(t)
	m := f.createMap(t, "inv-map", "invite", false)

	if err := f.mapsSvc.GrantSpectatorInvite(context.Background(), m.ID, f.playerID, f.designerID); err != nil {
		t.Fatalf("grant invite: %v", err)
	}

	c := f.dial(t)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, f.playerJWT, proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbSpectate, encodeSpectate(uint32(m.ID), "", proto.SpectateModeFreeCam, 0))); err != nil {
		t.Fatal(err)
	}

	conn := f.awaitConn(t)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn.Subscription != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn.Subscription == nil {
		t.Fatal("invited player spectator should be allowed")
	}
	if conn.Subscription.Policy != aoi.PolicySpectator {
		t.Errorf("Subscription.Policy: got %v, want PolicySpectator", conn.Subscription.Policy)
	}
}

// ---- sandbox instances: designer-only ----

func TestSpectate_SandboxInstance_PlayerRejected(t *testing.T) {
	f := newSpectateFixture(t)
	m := f.createMap(t, "pub-map-for-sandbox", "public", true)

	c := f.dial(t)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, f.playerJWT, proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	hint := "sandbox:" + itoa(f.designerID) + ":" + itoa(m.ID)
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbSpectate, encodeSpectate(uint32(m.ID), hint, proto.SpectateModeFreeCam, 0))); err != nil {
		t.Fatal(err)
	}

	conn := f.awaitConn(t)
	time.Sleep(150 * time.Millisecond)
	if conn.Subscription != nil {
		t.Errorf("sandbox spectate from player realm should be rejected even on public map; Subscription was attached")
	}
}

// ---- service-level invite tests ----

func TestSpectatorInvites_GrantAndRevoke(t *testing.T) {
	f := newSpectateFixture(t)
	m := f.createMap(t, "inv-map-svc", "invite", false)

	ctx := context.Background()

	allowed, err := f.mapsSvc.IsPlayerSpectatorAllowed(ctx, m.ID, f.playerID, "invite")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("no invite yet, must not be allowed")
	}

	if err := f.mapsSvc.GrantSpectatorInvite(ctx, m.ID, f.playerID, f.designerID); err != nil {
		t.Fatal(err)
	}

	// Idempotency: re-granting must not error.
	if err := f.mapsSvc.GrantSpectatorInvite(ctx, m.ID, f.playerID, f.designerID); err != nil {
		t.Fatalf("re-grant should be idempotent: %v", err)
	}

	allowed, err = f.mapsSvc.IsPlayerSpectatorAllowed(ctx, m.ID, f.playerID, "invite")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("after grant, must be allowed")
	}

	invites, err := f.mapsSvc.ListSpectatorInvites(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(invites) != 1 {
		t.Fatalf("invites: got %d, want 1", len(invites))
	}
	if invites[0].PlayerID != f.playerID || invites[0].GrantedBy != f.designerID {
		t.Errorf("invite shape mismatch: %+v", invites[0])
	}

	if err := f.mapsSvc.RevokeSpectatorInvite(ctx, m.ID, f.playerID); err != nil {
		t.Fatal(err)
	}

	allowed, err = f.mapsSvc.IsPlayerSpectatorAllowed(ctx, m.ID, f.playerID, "invite")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("after revoke, must not be allowed")
	}
}

func TestSpectatorInvites_PolicyDispatch(t *testing.T) {
	f := newSpectateFixture(t)
	m := f.createMap(t, "policy-dispatch-map", "public", true)
	ctx := context.Background()

	// Public always allowed (no DB hit).
	allowed, err := f.mapsSvc.IsPlayerSpectatorAllowed(ctx, m.ID, f.playerID, "public")
	if err != nil || !allowed {
		t.Errorf("public: allowed=%v err=%v", allowed, err)
	}

	// Private always rejected (no DB hit).
	allowed, err = f.mapsSvc.IsPlayerSpectatorAllowed(ctx, m.ID, f.playerID, "private")
	if err != nil || allowed {
		t.Errorf("private: allowed=%v err=%v", allowed, err)
	}

	// Unknown policy fails closed.
	_, err = f.mapsSvc.IsPlayerSpectatorAllowed(ctx, m.ID, f.playerID, "garbage")
	if err == nil {
		t.Errorf("unknown policy: want error, got nil")
	}
}
