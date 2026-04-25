package ws

import (
	"context"
	"errors"
	"fmt"

	"boxland/server/internal/proto"
)

// VerbHandler runs one verb. The handler is called inside the gateway's
// per-connection read loop; it must NOT block. Handlers that need to do
// I/O dispatch goroutines internally.
//
// payload is the raw bytes from ClientMessage.payload; handlers are
// responsible for decoding it as the verb-specific FlatBuffers table.
type VerbHandler func(ctx context.Context, conn *Connection, payload []byte) error

// Dispatcher routes ClientMessage envelopes to per-verb handlers. Holds
// a realm-tag check before invoking the handler so designer-only verbs
// (DesignerCommand) can never be dispatched on a player-realm connection.
type Dispatcher struct {
	playerHandlers   map[proto.Verb]VerbHandler
	designerHandlers map[proto.Verb]VerbHandler // additive: designer connections can do everything player can + DesignerCommand
}

// NewDispatcher returns an empty dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		playerHandlers:   make(map[proto.Verb]VerbHandler),
		designerHandlers: make(map[proto.Verb]VerbHandler),
	}
}

// HandlePlayer registers a handler for a verb dispatched on player or
// designer realms (designers can do anything players can; the additive
// model means we don't have to register handlers twice).
func (d *Dispatcher) HandlePlayer(v proto.Verb, h VerbHandler) {
	d.playerHandlers[v] = h
}

// HandleDesigner registers a handler for a verb dispatched ONLY on
// designer realm. Used for DesignerCommand and any future designer-only
// opcodes.
func (d *Dispatcher) HandleDesigner(v proto.Verb, h VerbHandler) {
	d.designerHandlers[v] = h
}

// Dispatch processes one decoded envelope. Returns an error iff the verb
// is unhandled, the realm is wrong, or the handler returned one. Errors
// are logged + the connection is closed in the gateway loop.
func (d *Dispatcher) Dispatch(ctx context.Context, conn *Connection, msg *proto.ClientMessage) error {
	verb := msg.Verb()
	if verb == proto.VerbNone {
		return errors.New("dispatcher: VerbNone is reserved")
	}

	// Designer-only handlers checked first; they require designer realm.
	if h, ok := d.designerHandlers[verb]; ok {
		if conn.Realm() != RealmDesigner {
			return fmt.Errorf("dispatcher: verb %s requires designer realm; have %s",
				verb, conn.Realm())
		}
		return h(ctx, conn, msg.PayloadBytes())
	}

	if h, ok := d.playerHandlers[verb]; ok {
		return h(ctx, conn, msg.PayloadBytes())
	}

	return fmt.Errorf("dispatcher: unhandled verb %s", verb)
}
