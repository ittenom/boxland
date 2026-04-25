package ws_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"boxland/server/internal/proto"
	"boxland/server/internal/sim/aoi"
	"boxland/server/internal/sim/spatial"
	boxws "boxland/server/internal/ws"
)

func TestBroadcaster_PolicyDefaults(t *testing.T) {
	auth := &fakeAuth{playerSubject: 1, designerSubject: 2}
	d := boxws.NewDispatcher()
	d.HandlePlayer(proto.VerbHeartbeat, func(context.Context, *boxws.Connection, []byte) error { return nil })

	g := boxws.NewGateway(auth, d, boxws.Options{})
	srv := httptest.NewServer(g.HTTPHandler())
	defer srv.Close()

	br := boxws.NewBroadcaster(g, func(*boxws.Connection, boxws.BroadcastPolicy) ([]byte, error) {
		return nil, nil
	})

	dial := func(realm proto.Realm) *websocket.Conn {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		_ = c.Write(ctx, websocket.MessageBinary, encodeAuth(realm, "tok", proto.ClientKindWeb, ""))
		_ = c.Write(ctx, websocket.MessageBinary, encodeClientMessage(proto.VerbHeartbeat, nil))
		return c
	}
	playerC := dial(proto.RealmPlayer)
	defer playerC.Close(websocket.StatusNormalClosure, "")
	designerC := dial(proto.RealmDesigner)
	defer designerC.Close(websocket.StatusNormalClosure, "")

	for i := 0; i < 50 && len(g.Conns()) < 2; i++ {
		time.Sleep(20 * time.Millisecond)
	}

	for _, c := range g.Conns() {
		got := br.PolicyFor(c)
		switch c.Realm() {
		case boxws.RealmPlayer:
			if got != boxws.BroadcastPlayer {
				t.Errorf("player conn default: got %v, want BroadcastPlayer", got)
			}
		case boxws.RealmDesigner:
			if got != boxws.BroadcastDesigner {
				t.Errorf("designer conn default: got %v, want BroadcastDesigner", got)
			}
		}
	}

	// Override: a designer can be flagged as a spectator.
	for _, c := range g.Conns() {
		if c.Realm() == boxws.RealmDesigner {
			br.SetPolicy(c.ID(), boxws.BroadcastSpectator)
			if br.PolicyFor(c) != boxws.BroadcastSpectator {
				t.Errorf("override didn't take")
			}
			br.Forget(c.ID())
			if br.PolicyFor(c) != boxws.BroadcastDesigner {
				t.Errorf("after Forget, default should kick back in")
			}
		}
	}
}

func TestBroadcaster_Tick_DeliversBlobToSubscribedConn(t *testing.T) {
	auth := &fakeAuth{playerSubject: 1}
	d := boxws.NewDispatcher()
	d.HandlePlayer(proto.VerbHeartbeat, func(context.Context, *boxws.Connection, []byte) error { return nil })

	g := boxws.NewGateway(auth, d, boxws.Options{})
	srv := httptest.NewServer(g.HTTPHandler())
	defer srv.Close()

	encodeCalls := atomic.Int64{}
	canned := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	br := boxws.NewBroadcaster(g, func(c *boxws.Connection, _ boxws.BroadcastPolicy) ([]byte, error) {
		encodeCalls.Add(1)
		return canned, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close(websocket.StatusNormalClosure, "")
	_ = cli.Write(ctx, websocket.MessageBinary, encodeAuth(proto.RealmPlayer, "tok", proto.ClientKindWeb, ""))
	_ = cli.Write(ctx, websocket.MessageBinary, encodeClientMessage(proto.VerbHeartbeat, nil))

	// Wait for the conn to register, then attach a Subscription so the
	// broadcaster considers it.
	for i := 0; i < 50 && len(g.Conns()) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	conns := g.Conns()
	if len(conns) != 1 {
		t.Fatalf("expected 1 conn, got %d", len(conns))
	}
	conns[0].Subscription = aoi.NewSubscription(1, aoi.PolicyPlayer, spatial.MakeChunkID(0, 0), 1)

	br.Tick(context.Background())

	// Read the blob the broadcaster pushed.
	readCtx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer rcancel()
	mt, body, err := cli.Read(readCtx)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if mt != websocket.MessageBinary {
		t.Errorf("got message type %v, want binary", mt)
	}
	if string(body) != string(canned) {
		t.Errorf("blob mismatch: got %v, want %v", body, canned)
	}
	if encodeCalls.Load() != 1 {
		t.Errorf("encoder calls: got %d, want 1", encodeCalls.Load())
	}
}

func TestBroadcaster_Tick_SkipsConnsWithoutSubscription(t *testing.T) {
	auth := &fakeAuth{playerSubject: 1}
	d := boxws.NewDispatcher()
	d.HandlePlayer(proto.VerbHeartbeat, func(context.Context, *boxws.Connection, []byte) error { return nil })

	g := boxws.NewGateway(auth, d, boxws.Options{})
	srv := httptest.NewServer(g.HTTPHandler())
	defer srv.Close()

	encodeCalls := atomic.Int64{}
	br := boxws.NewBroadcaster(g, func(c *boxws.Connection, _ boxws.BroadcastPolicy) ([]byte, error) {
		encodeCalls.Add(1)
		return nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, _, _ := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	defer cli.Close(websocket.StatusNormalClosure, "")
	_ = cli.Write(ctx, websocket.MessageBinary, encodeAuth(proto.RealmPlayer, "tok", proto.ClientKindWeb, ""))
	_ = cli.Write(ctx, websocket.MessageBinary, encodeClientMessage(proto.VerbHeartbeat, nil))

	for i := 0; i < 50 && len(g.Conns()) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	br.Tick(context.Background())
	if encodeCalls.Load() != 0 {
		t.Errorf("encoder should NOT have been called for un-subscribed conn; calls=%d", encodeCalls.Load())
	}
}
