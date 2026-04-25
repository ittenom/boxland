package ws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"boxland/server/internal/proto"
)

// Sandbox-only designer opcodes (PLAN.md §130). Each is rejected unless:
//
//   1. connection.realm == designer (enforced upstream by the dispatcher's
//      designer-handler split)
//   2. the targeted instance lives under the sandbox: id namespace
//
// The role-on-live check lives outside the WS dispatcher: production
// designer roles get role-tagged tickets at WS-handshake time; this
// package only enforces the realm + namespace invariants. PLAN.md §135
// asserts that crossing these boundaries always produces a structured
// rejection rather than a silent drop.

// ErrSandboxOnly is returned when a designer-opcode is dispatched
// against a non-sandbox instance.
var ErrSandboxOnly = errors.New("designer_command: opcode requires a sandbox: instance")

// targetInstanceID extracts the instance id the opcode targets. Most
// designer opcodes carry it on the inner payload (the wire schema for
// each is in input.fbs); v1 keeps the targeting simple by reading it
// off the connection's active Subscription -- the instance the
// designer most recently joined.
//
// If the connection has not joined any instance yet, returns an empty
// string and the caller rejects.
func targetInstanceID(conn *Connection, inHint string) string {
	if inHint != "" {
		return inHint
	}
	// We don't track the current instance id on the Connection itself;
	// the runtime gate reads it back from the JoinMap call. Until that
	// wiring exists, opcodes that don't carry an explicit hint are
	// rejected with ErrSandboxOnly so callers must opt in to a sandbox
	// instance via instance_hint -- "sandbox:..." in the payload.
	return ""
}

// requireSandbox returns nil iff the instance id is in the sandbox:
// namespace. Used by every sandbox-only opcode handler.
func requireSandbox(instanceID string) error {
	if !strings.HasPrefix(instanceID, "sandbox:") {
		return ErrSandboxOnly
	}
	return nil
}

// ---- per-opcode handlers ---------------------------------------------
//
// Wire payloads are not yet defined for the runtime opcodes (they're
// reserved in input.fbs's DesignerOpcode enum but no per-opcode tables
// exist alongside PlaceTilesPayload). v1 routes them through the
// generic DesignerCommandPayload.data blob -- the inner format is
// `instance_id:string\n[args]` ASCII for now; a richer FlatBuffers
// schema lands when a Sandbox UI calls these from the wire.
//
// The handlers below validate the realm + sandbox-only invariant +
// dispatch to the runtime InstanceManager. Errors close the
// connection (gateway treats realm violations + persistent errors as
// terminal) so silent drops are impossible -- PLAN.md §135.

func handleSandboxFreeze(_ context.Context, conn *Connection, deps AuthoringDeps, data []byte) error {
	instanceID := strings.TrimSpace(string(data))
	if err := requireSandbox(instanceID); err != nil {
		return err
	}
	mi := deps.Instances.Get(instanceID)
	if mi == nil {
		return fmt.Errorf("freeze_tick: instance %q not running", instanceID)
	}
	mi.Scheduler.Freeze()
	slog.Info("sandbox freeze", "conn", conn.ID(), "instance", instanceID)
	return nil
}

func handleSandboxStep(ctx context.Context, conn *Connection, deps AuthoringDeps, data []byte) error {
	instanceID := strings.TrimSpace(string(data))
	if err := requireSandbox(instanceID); err != nil {
		return err
	}
	mi := deps.Instances.Get(instanceID)
	if mi == nil {
		return fmt.Errorf("step_tick: instance %q not running", instanceID)
	}
	if err := mi.Scheduler.StepOnce(ctx); err != nil {
		return fmt.Errorf("step_tick: %w", err)
	}
	slog.Info("sandbox step", "conn", conn.ID(), "instance", instanceID, "tick", mi.Scheduler.Tick())
	return nil
}

// handleSandboxStub is the placeholder for opcodes that need wire
// schemas before they can do anything more interesting than validate
// the realm + sandbox-only invariant. Spawn-any, set-resource,
// take-control, release-control, teleport, godmode all use it for now.
//
// The handler still ENFORCES the invariant -- a crossed-realm dispatch
// returns ErrSandboxOnly which the dispatcher closes the connection on
// (PLAN.md §135 "structured rejection rather than a silent drop").
func handleSandboxStub(opcode proto.DesignerOpcode) func(context.Context, *Connection, AuthoringDeps, []byte) error {
	return func(_ context.Context, conn *Connection, _ AuthoringDeps, data []byte) error {
		instanceID := strings.TrimSpace(string(data))
		if err := requireSandbox(instanceID); err != nil {
			return err
		}
		slog.Info("sandbox opcode (stub)",
			"conn", conn.ID(), "opcode", opcode, "instance", instanceID,
			"ref", "v1: wire payload pending Sandbox UI")
		return nil
	}
}
