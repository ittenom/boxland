// Verb handlers for the ClientMessage envelope. One file per
// surface area is overkill at v1's verb count; everything lives here.
//
// Handlers are registered onto a Dispatcher in cmd/boxland/main.go; this
// file just declares the implementations + the canonical default
// registration helper.
package ws

import (
	"context"
	"errors"
	"log/slog"

	"boxland/server/internal/proto"
)

// RegisterDefaultVerbs wires the standard handler set onto a dispatcher.
// Surfaces with custom dispatch (e.g. Sandbox adding designer-only ops
// to the same dispatcher) extend the result.
func RegisterDefaultVerbs(d *Dispatcher) {
	d.HandlePlayer(proto.VerbJoinMap, handleJoinMap)
	d.HandlePlayer(proto.VerbLeaveMap, handleLeaveMap)
	d.HandlePlayer(proto.VerbMove, handleMove)
	d.HandlePlayer(proto.VerbInteract, handleInteract)
	d.HandlePlayer(proto.VerbHeartbeat, handleHeartbeat)
	d.HandlePlayer(proto.VerbAckTick, handleAckTick)
	d.HandlePlayer(proto.VerbSpectate, handleSpectate)
	d.HandleDesigner(proto.VerbDesignerCommand, handleDesignerCommand)
}

func handleJoinMap(_ context.Context, conn *Connection, payload []byte) error {
	if len(payload) < 8 {
		return errors.New("join_map: short payload")
	}
	jp := proto.GetRootAsJoinMapPayload(payload, 0)
	slog.Info("ws join_map", "conn", conn.ID(),
		"map_id", jp.MapId(), "instance_hint", string(jp.InstanceHint()),
	)
	// Real subscription wiring lands when the broadcaster joins this
	// to a live world (task #95). For now the handler proves the
	// dispatch path works.
	return nil
}

func handleLeaveMap(_ context.Context, conn *Connection, payload []byte) error {
	slog.Info("ws leave_map", "conn", conn.ID())
	return nil
}

func handleMove(_ context.Context, conn *Connection, payload []byte) error {
	if len(payload) < 8 {
		return errors.New("move: short payload")
	}
	mp := proto.GetRootAsMovePayload(payload, 0)
	slog.Debug("ws move", "conn", conn.ID(), "vx", mp.Vx(), "vy", mp.Vy())
	// Apply intent to the entity owned by this connection. Wiring to the
	// World + the Velocity store happens in task #95 alongside the
	// per-tick broadcaster.
	return nil
}

func handleInteract(_ context.Context, conn *Connection, payload []byte) error {
	if len(payload) < 8 {
		return errors.New("interact: short payload")
	}
	ip := proto.GetRootAsInteractPayload(payload, 0)
	slog.Info("ws interact", "conn", conn.ID(), "target", ip.TargetEntityId())
	return nil
}

func handleHeartbeat(_ context.Context, conn *Connection, payload []byte) error {
	// Heartbeat just resets the read deadline. Acknowledge by no-op; the
	// gateway's per-message timeout sees the read happen.
	return nil
}

func handleAckTick(_ context.Context, conn *Connection, payload []byte) error {
	if len(payload) < 8 {
		return errors.New("ack_tick: short payload")
	}
	ap := proto.GetRootAsAckTickPayload(payload, 0)
	slog.Debug("ws ack_tick", "conn", conn.ID(), "tick", ap.LastAppliedTick())
	// Used by reconnect handshake (task #96): the gateway compares the
	// ack against current tick and either resends Diffs or a fresh
	// Snapshot. Stub here; real handling lands with the broadcaster.
	return nil
}

func handleSpectate(_ context.Context, conn *Connection, payload []byte) error {
	if conn.Realm() != RealmPlayer && conn.Realm() != RealmDesigner {
		return errors.New("spectate: requires authenticated realm")
	}
	if len(payload) < 8 {
		return errors.New("spectate: short payload")
	}
	sp := proto.GetRootAsSpectatePayload(payload, 0)
	slog.Info("ws spectate", "conn", conn.ID(), "mode", sp.Mode())
	return nil
}

func handleDesignerCommand(_ context.Context, conn *Connection, payload []byte) error {
	if len(payload) < 8 {
		return errors.New("designer_command: short payload")
	}
	dc := proto.GetRootAsDesignerCommandPayload(payload, 0)
	slog.Info("ws designer_command", "conn", conn.ID(), "opcode", dc.Opcode())
	// Real opcode dispatch (spawn-any, set-resource, freeze-tick, ...)
	// lands in task #130 alongside the Sandbox surface.
	return nil
}
