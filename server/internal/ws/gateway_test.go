package ws_test

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"boxland/server/internal/proto"
	boxws "boxland/server/internal/ws"
)

// fakeAuth lets tests script the auth-handshake outcomes without going
// through the real Postgres-backed services.
type fakeAuth struct {
	playerSubject  boxws.SubjectID
	playerErr      error
	designerSubject boxws.SubjectID
	designerErr    error
}

func (f *fakeAuth) VerifyPlayer(token string) (boxws.SubjectID, error) {
	if f.playerErr != nil {
		return 0, f.playerErr
	}
	return f.playerSubject, nil
}
func (f *fakeAuth) RedeemDesignerTicket(_ context.Context, _ string, _ net.IP) (boxws.SubjectID, error) {
	if f.designerErr != nil {
		return 0, f.designerErr
	}
	return f.designerSubject, nil
}

// startGateway spins up an httptest server hosting the gateway + returns a
// helper that opens a fresh client connection to it.
func startGateway(t *testing.T, auth boxws.AuthBackend, dispatcher *boxws.Dispatcher) (*httptest.Server, func() *websocket.Conn) {
	t.Helper()
	g := boxws.NewGateway(auth, dispatcher, boxws.Options{})
	srv := httptest.NewServer(g.HTTPHandler())
	t.Cleanup(srv.Close)
	dial := func() *websocket.Conn {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return c
	}
	return srv, dial
}

func TestGateway_AuthHandshake_PlayerSuccess(t *testing.T) {
	auth := &fakeAuth{playerSubject: 42}
	d := boxws.NewDispatcher()
	calls := atomic.Int64{}
	d.HandlePlayer(proto.VerbHeartbeat, func(_ context.Context, conn *boxws.Connection, _ []byte) error {
		calls.Add(1)
		return nil
	})
	_, dial := startGateway(t, auth, d)

	c := dial()
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, "test-jwt", proto.ClientKindWeb, "1.0.0")); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	// Send a heartbeat to prove dispatch works after auth.
	if err := c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbHeartbeat, nil)); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	// Wait briefly for the gateway to process.
	for i := 0; i < 20 && calls.Load() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Errorf("heartbeat handler not invoked; calls=%d", calls.Load())
	}
}

func TestGateway_AuthHandshake_RejectsBadToken(t *testing.T) {
	auth := &fakeAuth{playerErr: errors.New("invalid jwt")}
	d := boxws.NewDispatcher()
	_, dial := startGateway(t, auth, d)

	c := dial()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, "bad", proto.ClientKindWeb, ""))

	// The gateway should close with policy violation.
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("expected close after bad auth")
	}
	if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation {
		t.Errorf("close status: got %d, want %d", status, websocket.StatusPolicyViolation)
	}
}

func TestGateway_DesignerOnlyVerb_RejectedOnPlayerRealm(t *testing.T) {
	auth := &fakeAuth{playerSubject: 1, designerSubject: 99}
	d := boxws.NewDispatcher()
	d.HandleDesigner(proto.VerbDesignerCommand, func(context.Context, *boxws.Connection, []byte) error {
		t.Error("designer handler should NOT have run on player realm")
		return nil
	})
	_, dial := startGateway(t, auth, d)

	c := dial()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmPlayer, "ok", proto.ClientKindWeb, ""))
	_ = c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbDesignerCommand, encodeDesignerCommand(proto.DesignerOpcodeSpawnAny)))

	// The gateway closes the connection on realm violation.
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("expected close after realm violation")
	}
	if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation {
		t.Errorf("close status: got %d, want %d", status, websocket.StatusPolicyViolation)
	}
}

func TestGateway_DesignerVerbAcceptedOnDesignerRealm(t *testing.T) {
	auth := &fakeAuth{designerSubject: 99}
	d := boxws.NewDispatcher()
	called := atomic.Bool{}
	d.HandleDesigner(proto.VerbDesignerCommand, func(_ context.Context, conn *boxws.Connection, _ []byte) error {
		called.Store(true)
		if conn.Realm() != boxws.RealmDesigner {
			t.Errorf("conn.realm: got %v", conn.Realm())
		}
		return nil
	})
	_, dial := startGateway(t, auth, d)

	c := dial()
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageBinary,
		encodeAuth(proto.RealmDesigner, "ticket", proto.ClientKindWeb, ""))
	_ = c.Write(ctx, websocket.MessageBinary,
		encodeClientMessage(proto.VerbDesignerCommand, encodeDesignerCommand(proto.DesignerOpcodeSpawnAny)))

	for i := 0; i < 20 && !called.Load(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !called.Load() {
		t.Error("designer handler not invoked on designer realm")
	}
}

func TestGateway_RegistryReportsLiveConnections(t *testing.T) {
	auth := &fakeAuth{playerSubject: 5}
	d := boxws.NewDispatcher()
	d.HandlePlayer(proto.VerbHeartbeat, func(context.Context, *boxws.Connection, []byte) error { return nil })

	g := boxws.NewGateway(auth, d, boxws.Options{})
	srv := httptest.NewServer(g.HTTPHandler())
	defer srv.Close()

	// Open 3 conns concurrently; each completes the auth handshake.
	var wg sync.WaitGroup
	clients := make([]*websocket.Conn, 3)
	for i := range clients {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				t.Errorf("dial %d: %v", i, err)
				return
			}
			clients[i] = c
			_ = c.Write(ctx, websocket.MessageBinary,
				encodeAuth(proto.RealmPlayer, "tok", proto.ClientKindWeb, ""))
			_ = c.Write(ctx, websocket.MessageBinary,
				encodeClientMessage(proto.VerbHeartbeat, nil))
		}()
	}
	wg.Wait()

	// Wait for the gateway to register all 3.
	for i := 0; i < 50 && len(g.Conns()) < 3; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if got := len(g.Conns()); got != 3 {
		t.Errorf("Conns count: got %d, want 3", got)
	}

	// Cleanup.
	for _, c := range clients {
		if c != nil {
			_ = c.Close(websocket.StatusNormalClosure, "test done")
		}
	}
}
