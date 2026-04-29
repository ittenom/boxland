package ws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/proto"
	"boxland/server/internal/sim/editor"
)

// editor_authoring.go — Editor session opcodes (3xx + 4xx + 5xx).
//
// Wires the new DesignerCommand opcodes (EditorJoin*, PlaceLevelEntity,
// MoveLevelEntity, RemoveLevelEntity, etc.) onto the dispatcher. Each
// opcode routes through the global SessionManager so two designers
// editing the same target see each other's changes live.
//
// Snapshot encoding (the EditorSnapshot FlatBuffer) is the responsibility
// of a sibling file (editor_snapshot.go); this file owns the dispatch +
// per-op application.

// EditorAuthoringDeps is the narrow service surface the editor opcode
// handlers need. Wired by main.go alongside the existing AuthoringDeps.
type EditorAuthoringDeps struct {
	Sessions *editor.Manager
	Levels   *levels.Service
	Maps     *mapsservice.Service

	// Entities + Assets are the catalog services used by the
	// snapshot builder to populate `theme[]` and `palette[]`. Both
	// are optional; when nil those vectors ship empty.
	Entities *entities.Service
	Assets   *assets.Service
}

// nextSubscriberID is a process-global monotonic counter for
// editor.SubscriberID values. We don't need a per-session ID space —
// IDs are scoped by `(session, id)` at the manager level — so a
// shared atomic is the simplest correct shape.
var nextSubscriberID atomic.Uint64

// connEditor is the per-connection editor state we attach to a WS
// Connection when the designer joins an editor surface. One
// Connection can be subscribed to one editor session at a time
// (joining a second target leaves the first).
type connEditor struct {
	mu        sync.Mutex
	key       editor.SessionKey
	subID     editor.SubscriberID
	unsub     func()
	sink      chan editor.Diff
	pumpStop  chan struct{}
	hasJoined bool
}

// connEditors maps WS Connection -> its editor state. We keep this as
// a separate map (instead of stuffing it on Connection) because the
// editor surface is one of several optional WS extensions; the
// Connection type stays narrow.
var (
	connEditorsMu sync.Mutex
	connEditors   = map[*Connection]*connEditor{}
)

// connEditorFor returns the editor state for `conn`, allocating it
// on first use.
func connEditorFor(conn *Connection) *connEditor {
	connEditorsMu.Lock()
	defer connEditorsMu.Unlock()
	if ce, ok := connEditors[conn]; ok {
		return ce
	}
	ce := &connEditor{}
	connEditors[conn] = ce
	return ce
}

// dropConnEditor tears down + removes the editor state for `conn`.
// Called when the WS closes; idempotent.
func dropConnEditor(conn *Connection) {
	connEditorsMu.Lock()
	ce, ok := connEditors[conn]
	if ok {
		delete(connEditors, conn)
	}
	connEditorsMu.Unlock()
	if ce != nil {
		ce.leave()
	}
}

// dispatchEditorOpcode routes one of the editor opcodes to its
// handler. Returns true when the opcode was recognized + handled
// (success or error); the caller falls back to other dispatch
// paths when it returns false. Mirrors the in-page switch shape
// of the existing dispatchDesignerCommand for consistency.
func dispatchEditorOpcode(
	ctx context.Context,
	conn *Connection,
	deps EditorAuthoringDeps,
	opcode proto.DesignerOpcode,
	data []byte,
) (handled bool, err error) {
	switch opcode {
	case proto.DesignerOpcodeEditorJoinMapmaker:
		return true, handleEditorJoin(ctx, conn, deps, editor.KindMapmaker, data)
	case proto.DesignerOpcodeEditorJoinLevelEditor:
		return true, handleEditorJoin(ctx, conn, deps, editor.KindLevelEditor, data)
	case proto.DesignerOpcodeEditorLeave:
		dropConnEditor(conn)
		return true, nil
	case proto.DesignerOpcodeEditorUndo:
		return true, handleEditorUndo(ctx, conn, deps)
	case proto.DesignerOpcodeEditorRedo:
		return true, handleEditorRedo(ctx, conn, deps)

	case proto.DesignerOpcodePlaceLevelEntity:
		return true, handlePlaceLevelEntity(ctx, conn, deps, data)
	case proto.DesignerOpcodeMoveLevelEntity:
		return true, handleMoveLevelEntity(ctx, conn, deps, data)
	case proto.DesignerOpcodeRemoveLevelEntity:
		return true, handleRemoveLevelEntity(ctx, conn, deps, data)
	case proto.DesignerOpcodeSetLevelEntityOverrides:
		return true, handleSetLevelEntityOverrides(ctx, conn, deps, data)

	// Mapmaker session ops. The legacy handlePlaceTiles /
	// handleEraseTiles in authoring.go remain as a fallback for
	// designers who haven't called EditorJoinMapmaker (e.g. test
	// connections), but real editor sessions route through here so
	// the change is broadcast to every sibling tab via the
	// session's diff stream.
	case proto.DesignerOpcodePlaceTiles:
		if !connHasMapmakerSession(conn) {
			return false, nil
		}
		return true, handleSessionPlaceTiles(ctx, conn, deps, data)
	case proto.DesignerOpcodeEraseTiles:
		if !connHasMapmakerSession(conn) {
			return false, nil
		}
		return true, handleSessionEraseTiles(ctx, conn, deps, data)
	case proto.DesignerOpcodeLockTiles:
		if !connHasMapmakerSession(conn) {
			return false, nil
		}
		return true, handleSessionLockTiles(ctx, conn, deps, data)
	case proto.DesignerOpcodeUnlockTiles:
		if !connHasMapmakerSession(conn) {
			return false, nil
		}
		return true, handleSessionUnlockTiles(ctx, conn, deps, data)
	}
	return false, nil
}

// connHasMapmakerSession reports whether `conn` has joined a
// mapmaker editor session. Used to decide between the new
// session-routed handlers and the legacy direct-persist path.
func connHasMapmakerSession(conn *Connection) bool {
	connEditorsMu.Lock()
	ce, ok := connEditors[conn]
	connEditorsMu.Unlock()
	if !ok || ce == nil {
		return false
	}
	ce.mu.Lock()
	defer ce.mu.Unlock()
	return ce.hasJoined && ce.key.Kind == editor.KindMapmaker
}

// handleEditorJoin: the designer just opened an editor; subscribe to
// the target's session + send back a snapshot. See
// editor_snapshot.go for the encoder.
func handleEditorJoin(ctx context.Context, conn *Connection, deps EditorAuthoringDeps, kind editor.Kind, data []byte) error {
	if deps.Sessions == nil {
		return errors.New("editor_join: sessions manager not configured")
	}
	if len(data) < 4 {
		return errors.New("editor_join: short payload")
	}
	p := proto.GetRootAsEditorJoinPayload(data, 0)
	targetID := int64(p.TargetId())
	if targetID <= 0 {
		return fmt.Errorf("editor_join: bad target_id %d", targetID)
	}
	key := editor.SessionKey{Kind: kind, TargetID: targetID}

	ce := connEditorFor(conn)
	ce.mu.Lock()
	if ce.hasJoined {
		ce.mu.Unlock()
		ce.leave()
		ce.mu.Lock()
	}
	ce.key = key
	ce.subID = editor.SubscriberID(nextSubscriberID.Add(1))
	ce.sink = make(chan editor.Diff, 64)
	ce.pumpStop = make(chan struct{})
	ce.hasJoined = true
	sess := deps.Sessions.GetOrCreate(key)
	ce.unsub = sess.Subscribe(ce.subID, ce.sink)
	ce.mu.Unlock()

	go pumpEditorDiffs(conn, ce)

	// Snapshot is encoded by the sibling editor_snapshot.go.
	snap, err := buildEditorSnapshot(ctx, deps, kind, targetID)
	if err != nil {
		return fmt.Errorf("editor_join: build snapshot: %w", err)
	}
	if err := conn.SendEditorFrame(snap); err != nil {
		slog.Warn("editor_join: send snapshot", "err", err, "key", key)
		return err
	}
	return nil
}

// pumpEditorDiffs reads from ce.sink and pushes each diff to the
// WS conn. Stops when the conn closes or the connection's editor
// state is torn down via dropConnEditor.
func pumpEditorDiffs(conn *Connection, ce *connEditor) {
	for {
		select {
		case d, ok := <-ce.sink:
			if !ok {
				return
			}
			payload, err := encodeEditorDiff(d)
			if err != nil {
				slog.Warn("editor: encode diff", "err", err, "kind", d.Kind)
				continue
			}
			if err := conn.SendEditorFrame(payload); err != nil {
				slog.Warn("editor: send diff", "err", err)
				return
			}
		case <-ce.pumpStop:
			return
		}
	}
}

// leave unsubscribes + tears down the conn's editor state. Safe to
// call multiple times.
func (ce *connEditor) leave() {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	if !ce.hasJoined {
		return
	}
	if ce.unsub != nil {
		ce.unsub()
		ce.unsub = nil
	}
	if ce.pumpStop != nil {
		close(ce.pumpStop)
		ce.pumpStop = nil
	}
	if ce.sink != nil {
		// Drain + close so the pump goroutine exits cleanly.
		go func(s chan editor.Diff) {
			for range s {
			}
		}(ce.sink)
		close(ce.sink)
		ce.sink = nil
	}
	ce.hasJoined = false
}

// handleEditorUndo / Redo route through the conn's active session.
func handleEditorUndo(ctx context.Context, conn *Connection, deps EditorAuthoringDeps) error {
	sess, err := requireActiveSession(conn, deps)
	if err != nil {
		return err
	}
	_, err = sess.Undo(ctx, editor.Deps{Levels: deps.Levels, Maps: deps.Maps})
	return err
}

func handleEditorRedo(ctx context.Context, conn *Connection, deps EditorAuthoringDeps) error {
	sess, err := requireActiveSession(conn, deps)
	if err != nil {
		return err
	}
	_, err = sess.Redo(ctx, editor.Deps{Levels: deps.Levels, Maps: deps.Maps})
	return err
}

// requireActiveSession returns the editor.Session the conn is
// currently subscribed to, or an error if the conn isn't joined.
func requireActiveSession(conn *Connection, deps EditorAuthoringDeps) (*editor.Session, error) {
	ce := connEditorFor(conn)
	ce.mu.Lock()
	defer ce.mu.Unlock()
	if !ce.hasJoined {
		return nil, errors.New("editor: no active session (send EditorJoin first)")
	}
	sess := deps.Sessions.Find(ce.key)
	if sess == nil {
		return nil, fmt.Errorf("editor: session %s vanished", ce.key)
	}
	return sess, nil
}

// ---- Level placement handlers ----

func handlePlaceLevelEntity(ctx context.Context, conn *Connection, deps EditorAuthoringDeps, data []byte) error {
	if len(data) < 4 {
		return errors.New("place_level_entity: short payload")
	}
	p := proto.GetRootAsPlaceLevelEntityPayload(data, 0)
	levelID := int64(p.LevelId())
	sess, err := requireActiveSessionForLevel(conn, deps, levelID)
	if err != nil {
		return err
	}
	overrides := []byte(p.InstanceOverridesJson())
	tags := decodeStringVector(p.TagsLength, p.Tags)
	op := &editor.PlaceLevelEntityOp{
		LevelID:         levelID,
		EntityTypeID:    int64(p.EntityTypeId()),
		X:               p.X(),
		Y:               p.Y(),
		RotationDegrees: p.RotationDegrees(),
		Overrides:       overrides,
		Tags:            tags,
	}
	_, err = sess.Apply(ctx, editor.Deps{Levels: deps.Levels, Maps: deps.Maps}, op)
	return err
}

func handleMoveLevelEntity(ctx context.Context, conn *Connection, deps EditorAuthoringDeps, data []byte) error {
	if len(data) < 4 {
		return errors.New("move_level_entity: short payload")
	}
	p := proto.GetRootAsMoveLevelEntityPayload(data, 0)
	levelID := int64(p.LevelId())
	sess, err := requireActiveSessionForLevel(conn, deps, levelID)
	if err != nil {
		return err
	}
	op := &editor.MoveLevelEntityOp{
		LevelID:         levelID,
		PlacementID:     int64(p.PlacementId()),
		X:               p.X(),
		Y:               p.Y(),
		RotationDegrees: p.RotationDegrees(),
	}
	_, err = sess.Apply(ctx, editor.Deps{Levels: deps.Levels, Maps: deps.Maps}, op)
	return err
}

func handleRemoveLevelEntity(ctx context.Context, conn *Connection, deps EditorAuthoringDeps, data []byte) error {
	if len(data) < 4 {
		return errors.New("remove_level_entity: short payload")
	}
	p := proto.GetRootAsRemoveLevelEntityPayload(data, 0)
	levelID := int64(p.LevelId())
	sess, err := requireActiveSessionForLevel(conn, deps, levelID)
	if err != nil {
		return err
	}
	op := &editor.RemoveLevelEntityOp{
		LevelID:     levelID,
		PlacementID: int64(p.PlacementId()),
	}
	_, err = sess.Apply(ctx, editor.Deps{Levels: deps.Levels, Maps: deps.Maps}, op)
	return err
}

func handleSetLevelEntityOverrides(ctx context.Context, conn *Connection, deps EditorAuthoringDeps, data []byte) error {
	if len(data) < 4 {
		return errors.New("set_overrides: short payload")
	}
	p := proto.GetRootAsSetLevelEntityOverridesPayload(data, 0)
	levelID := int64(p.LevelId())
	sess, err := requireActiveSessionForLevel(conn, deps, levelID)
	if err != nil {
		return err
	}
	op := &editor.SetLevelEntityOverridesOp{
		LevelID:     levelID,
		PlacementID: int64(p.PlacementId()),
		Overrides:   []byte(p.InstanceOverridesJson()),
	}
	_, err = sess.Apply(ctx, editor.Deps{Levels: deps.Levels, Maps: deps.Maps}, op)
	return err
}

// requireActiveSessionForLevel verifies the conn's joined session
// is the level editor session for `levelID`. Stops a designer who
// joined Level A from mutating Level B by spoofing a payload.
func requireActiveSessionForLevel(conn *Connection, deps EditorAuthoringDeps, levelID int64) (*editor.Session, error) {
	ce := connEditorFor(conn)
	ce.mu.Lock()
	defer ce.mu.Unlock()
	if !ce.hasJoined {
		return nil, errors.New("editor: no active session")
	}
	if ce.key.Kind != editor.KindLevelEditor || ce.key.TargetID != levelID {
		return nil, fmt.Errorf("editor: op targets level %d, but conn joined %s", levelID, ce.key)
	}
	sess := deps.Sessions.Find(ce.key)
	if sess == nil {
		return nil, fmt.Errorf("editor: session %s vanished", ce.key)
	}
	return sess, nil
}

// decodeStringVector pulls a [string] field out of a FlatBuffer
// table given its length getter + element getter. Used by the
// payload decoders that carry tag lists.
func decodeStringVector(length func() int, get func(int) []byte) []string {
	n := length()
	if n <= 0 {
		return nil
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = string(get(i))
	}
	return out
}
