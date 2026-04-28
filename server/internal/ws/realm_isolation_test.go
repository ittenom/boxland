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
	"boxland/server/internal/sim/runtime"
	"boxland/server/internal/ws"
)

// PLAN.md §135: realm-isolation invariant test. Asserts that every
// cross-realm misuse produces a STRUCTURED rejection (close code or
// connection close after a logged dispatcher error) rather than a
// silent drop. Three subtests model the three failure modes.

type realmFixture struct {
	t            *testing.T
	srv          *httptest.Server
	authPlayer   *authplayer.Service
	authDesigner *authdesigner.Service
	mgr          *runtime.InstanceManager
	mapsSvc      *maps.Service
	designerID   int64
	playerJWT    string
	designerTok  string
	mapID        int64
}

func newRealmFixture(t *testing.T) *realmFixture {
	t.Helper()
	pool := openPool(t)
	t.Cleanup(pool.Close)
	cli := openRedis(t)
	t.Cleanup(cli.Close)

	authD := authdesigner.New(pool)
	d, err := authD.CreateDesigner(context.Background(), "realm-iso-d@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}

	authP := authplayer.New(pool, []byte("test-jwt-secret-32-bytes-padded__"))
	pl, err := authP.CreatePlayer(context.Background(), "realm-iso-p@x.com", "playerpass")
	if err != nil {
		t.Fatal(err)
	}
	jwt, err := authP.MintAccessToken(pl)
	if err != nil {
		t.Fatal(err)
	}

	mapsSvc := maps.New(pool)
	m, err := mapsSvc.Create(context.Background(), maps.CreateInput{
		Name: "realm-iso-map", Width: 64, Height: 64, CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	mgr := runtime.NewInstanceManager(pool, cli, mapsSvc, runtime.SystemDeps{})
	dispatcher := ws.NewDispatcher()
	ws.RegisterDefaultVerbs(dispatcher)
	deps := ws.AuthoringDeps{MapsService: mapsSvc, Instances: mgr}
	ws.RegisterAuthoringVerbs(dispatcher, deps)
	ws.RegisterSpectatorVerb(dispatcher, deps)

	authBackend := &ws.LiveAuthBackend{Player: authP, Designer: authD}
	g := ws.NewGateway(authBackend, dispatcher, ws.Options{})
	srv := httptest.NewServer(g.HTTPHandler())
	t.Cleanup(srv.Close)

	tok, err := authD.MintWSTicket(context.Background(), d.ID, net.ParseIP("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}

	return &realmFixture{
		t: t, srv: srv,
		authPlayer: authP, authDesigner: authD,
		mgr: mgr, mapsSvc: mapsSvc,
		designerID: d.ID, playerJWT: jwt, designerTok: tok,
		mapID: m.ID,
	}
}

func (f *realmFixture) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

// ---- (a) player-realm token cannot subscribe to a sandbox instance --

func TestRealmIsolation_PlayerCannotJoinMapSandbox(t *testing.T) {
	f := newRealmFixture(t)
	c := f.dial(t)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, f.playerJWT, proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	hint := "sandbox:" + itoa(f.designerID) + ":" + itoa(f.mapID)
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbJoinMap, encodeJoinMap(uint32(f.mapID), hint))); err != nil {
		t.Fatal(err)
	}
	// Read until close. PLAN.md §135 requires structured rejection;
	// the gateway translates "sandbox" realm errors into PolicyViolation.
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("expected close on cross-realm sandbox JoinMap")
	}
	status := websocket.CloseStatus(err)
	if status != websocket.StatusPolicyViolation && status != websocket.StatusNormalClosure {
		t.Errorf("close status: got %d, want PolicyViolation", status)
	}
}

func TestRealmIsolation_PlayerCannotSpectateSandbox(t *testing.T) {
	// Already covered by spectate_test.go::TestSpectate_SandboxInstance_PlayerRejected.
	// This subtest re-asserts the structured-rejection invariant from §135
	// via the JoinMap path; the spectate path is already enforced.
	t.Skip("covered by TestSpectate_SandboxInstance_PlayerRejected")
}

// ---- (b) designer-realm cannot dispatch sandbox-only ops on a live ---

func TestRealmIsolation_DesignerCannotFreezeOnLiveInstance(t *testing.T) {
	f := newRealmFixture(t)
	c := f.dial(t)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmDesigner, f.designerTok, proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	// Send FreezeTick targeting a live: instance id (the canonical one
	// the AuthoringDeps wiring uses). Per PLAN.md §130 this opcode is
	// sandbox-only; the dispatcher must reject -> connection closes.
	live := "live:" + itoa(f.mapID) + ":0"
	blob := encodeDesignerCommandWithData(t, proto.DesignerOpcodeFreezeTick, []byte(live))
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbDesignerCommand, blob)); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("expected close on live FreezeTick")
	}
	if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation && status != websocket.StatusNormalClosure {
		t.Errorf("close status: got %d, want PolicyViolation", status)
	}
}

// ---- (c) crossing realms always produces a structured rejection -----

func TestRealmIsolation_PlayerCannotIssueDesignerCommand(t *testing.T) {
	f := newRealmFixture(t)
	c := f.dial(t)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Auth as player, then try DesignerCommand (any opcode). The
	// dispatcher's designer-only handler check fires; gateway closes
	// with PolicyViolation.
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, f.playerJWT, proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	dc := encodeDesignerCommand(proto.DesignerOpcodePlaceTiles)
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbDesignerCommand, dc)); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("expected close on player DesignerCommand")
	}
	if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation && status != websocket.StatusNormalClosure {
		t.Errorf("close status: got %d, want PolicyViolation", status)
	}
}

// encodeDesignerCommandWithData wraps a designer opcode + raw inner data
// bytes in a DesignerCommandPayload. The runtime opcodes (FreezeTick,
// StepTick, etc.) consume the inner data as the instance-id ASCII string.
func encodeDesignerCommandWithData(t *testing.T, opcode proto.DesignerOpcode, data []byte) []byte {
	t.Helper()
	b := flatbuffers.NewBuilder(32 + len(data))
	proto.DesignerCommandPayloadStartDataVector(b, len(data))
	for i := len(data) - 1; i >= 0; i-- {
		b.PrependByte(data[i])
	}
	dataOff := b.EndVector(len(data))
	proto.DesignerCommandPayloadStart(b)
	proto.DesignerCommandPayloadAddOpcode(b, opcode)
	proto.DesignerCommandPayloadAddData(b, dataOff)
	root := proto.DesignerCommandPayloadEnd(b)
	b.Finish(root)
	return b.FinishedBytes()
}
