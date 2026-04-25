package ws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"boxland/server/internal/auth/designer"
	"boxland/server/internal/auth/player"
	"boxland/server/internal/proto"
)

// AuthBackend is the small surface the gateway needs to validate Auth
// handshakes. Real implementations live in internal/auth/{player,designer};
// tests inject mocks.
type AuthBackend interface {
	// VerifyPlayer validates a player JWT and returns the player id.
	VerifyPlayer(token string) (SubjectID, error)
	// RedeemDesignerTicket consumes a one-shot WS ticket bound to designerID + ip.
	RedeemDesignerTicket(ctx context.Context, raw string, ip net.IP) (SubjectID, error)
}

// LiveAuthBackend wires the production auth services into the AuthBackend
// interface.
type LiveAuthBackend struct {
	Player   *player.Service
	Designer *designer.Service
}

// VerifyPlayer parses + validates the JWT and returns the player id.
func (l *LiveAuthBackend) VerifyPlayer(token string) (SubjectID, error) {
	claims, err := l.Player.ParseAccessToken(token)
	if err != nil {
		return 0, err
	}
	return SubjectID(claims.PlayerID), nil
}

// RedeemDesignerTicket consumes the ticket and returns the designer id.
func (l *LiveAuthBackend) RedeemDesignerTicket(ctx context.Context, raw string, ip net.IP) (SubjectID, error) {
	d, err := l.Designer.RedeemWSTicket(ctx, raw, ip)
	if err != nil {
		return 0, err
	}
	return SubjectID(d.ID), nil
}

// authHandshakeTimeout caps how long the client has to send the first Auth
// message. Real clients do this in <100ms; 5s is a generous slop.
const authHandshakeTimeout = 5 * time.Second

// performAuthHandshake reads the first WS frame, decodes it as Auth, and
// validates it. Mutates conn.realm/subject/clientKind/clientVer on success.
func (g *Gateway) performAuthHandshake(ctx context.Context, conn *Connection, peerIP net.IP) error {
	hsCtx, cancel := context.WithTimeout(ctx, authHandshakeTimeout)
	defer cancel()

	_, blob, err := conn.ws.Read(hsCtx)
	if err != nil {
		return fmt.Errorf("read auth: %w", err)
	}
	if len(blob) < 8 {
		return errors.New("auth: blob too short")
	}
	a := proto.GetRootAsAuth(blob, 0)

	pv := a.ProtocolVersion(nil)
	if pv == nil || pv.Major() != 1 {
		return errors.New("auth: protocol version mismatch")
	}

	tok := string(a.Token())
	if tok == "" {
		return errors.New("auth: token required")
	}
	conn.clientKind = ClientKind(a.ClientKind())
	conn.clientVer = string(a.ClientVersion())

	switch a.Realm() {
	case proto.RealmPlayer:
		subj, err := g.auth.VerifyPlayer(tok)
		if err != nil {
			return fmt.Errorf("auth: player token: %w", err)
		}
		conn.realm = RealmPlayer
		conn.subject = subj
	case proto.RealmDesigner:
		subj, err := g.auth.RedeemDesignerTicket(hsCtx, tok, peerIP)
		if err != nil {
			return fmt.Errorf("auth: designer ticket: %w", err)
		}
		conn.realm = RealmDesigner
		conn.subject = subj
	default:
		return fmt.Errorf("auth: unknown realm %d", a.Realm())
	}
	return nil
}
