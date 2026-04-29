// Package editor owns the server-side state for live editor
// sessions: per-(kind, target) Sessions that hold the editor's
// undo/redo stack, broadcast diffs to subscribers, and persist ops
// through the existing levels/maps services.
//
// Per the holistic redesign, this is what lets two designers
// co-edit the same map and see each other's changes live: every
// op routes through the session, the persisted change is
// broadcast as an EditorDiff to every subscriber, and the local
// client applies the diff (whether it's the originator or a
// sibling).
//
// Concurrency model: each Session has its own mutex; ops are
// serialized within a session. Cross-session calls (the
// SessionManager's lookup map) use a separate RWMutex so the
// global map doesn't bottleneck per-session work.
package editor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
)

// Kind discriminates the two editor surfaces.
type Kind uint8

const (
	KindMapmaker Kind = iota
	KindLevelEditor
)

// String — for logs.
func (k Kind) String() string {
	switch k {
	case KindMapmaker:
		return "mapmaker"
	case KindLevelEditor:
		return "level-editor"
	}
	return "unknown"
}

// SessionKey identifies one editor session. Two designers editing
// the same level share the same Session (the broadcast network).
type SessionKey struct {
	Kind     Kind
	TargetID int64 // map id (Mapmaker) or level id (LevelEditor)
}

// String — for logs / debug.
func (k SessionKey) String() string {
	return fmt.Sprintf("%s:%d", k.Kind, k.TargetID)
}

// SubscriberID is opaque to the session; the WS gateway issues one
// per connected designer, hands it to Subscribe(), and uses it to
// route diffs back to the connection.
type SubscriberID uint64

// Diff is the wire-shape-agnostic shape the session emits when
// state changes. The WS encoder turns this into an EditorDiff
// FlatBuffer envelope; tests can read them directly.
type Diff struct {
	Kind     DiffKind
	Body     any // typed body matching Kind; e.g. PlacementAdded -> *levels.LevelEntity
	UndoDepth uint32
	RedoDepth uint32
}

// DiffKind mirrors the FlatBuffers EditorDiffKind enum. Kept as
// its own Go type so the session manager doesn't depend on the
// proto package directly.
type DiffKind uint8

const (
	DiffNone DiffKind = iota
	DiffTilePlaced
	DiffTileErased
	DiffLockAdded
	DiffLockRemoved
	DiffPlacementAdded
	DiffPlacementMoved
	DiffPlacementRemoved
	DiffOverridesChanged
	DiffHistoryChanged
)

// Op is one undo-able operation. Apply persists the change,
// emits a Diff. Inverse() returns the Op that reverses it for
// the undo stack.
type Op interface {
	Apply(ctx context.Context, deps Deps) (Diff, error)
	Inverse(ctx context.Context, deps Deps) (Op, error)
	Describe() string
}

// MultiDiffOp is an optional extension Ops can implement to fan
// out per-cell diffs after a batch Apply. The Session calls
// ExtraDiffs() right after Apply (under the same lock) and
// broadcasts each one in addition to the headline Diff. Used by
// the mapmaker's PlaceTilesOp / EraseTilesOp so a 30-cell brush
// stroke ships 30 diffs to siblings (one per cell) but stays one
// undo entry.
type MultiDiffOp interface {
	Op
	ExtraDiffs() []Diff
}

// Deps is the narrow service surface a Session needs. Concrete
// services come from the WS handler that owns the session.
type Deps struct {
	Levels *levels.Service
	Maps   *mapsservice.Service
}

// Session holds the state for one editor target. Methods are
// safe for concurrent use — Apply/Undo/Redo serialize via mu;
// Subscribe/Unsubscribe use the same mu (not a separate sub-lock)
// because subscriber list mutations are rare relative to ops.
type Session struct {
	Key SessionKey

	mu          sync.Mutex
	undoStack   []Op
	redoStack   []Op
	subscribers map[SubscriberID]chan<- Diff

	// Closed when the session is being torn down. Subscribers
	// detect this and clean up; new ops on a closed session
	// return ErrSessionClosed.
	closed atomic.Bool
}

const undoLimit = 100

// NewSession allocates an empty session. The manager takes care
// of registering it; tests can instantiate directly.
func NewSession(key SessionKey) *Session {
	return &Session{
		Key:         key,
		subscribers: make(map[SubscriberID]chan<- Diff),
	}
}

// ErrSessionClosed is returned by ops on a torn-down session.
var ErrSessionClosed = errors.New("editor: session closed")

// Subscribe registers a diff sink. The channel must be buffered
// adequately by the caller (the session never blocks on send; if
// the channel is full, the diff is dropped and a warning is
// logged via the caller's logger — that's a v1 limitation, but
// it keeps slow consumers from stalling the broadcast).
//
// Returns an Unsubscribe function. Idempotent in tear-down.
func (s *Session) Subscribe(id SubscriberID, sink chan<- Diff) (unsub func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers[id] = sink
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subscribers, id)
	}
}

// SubscriberCount — for tests + status indicators.
func (s *Session) SubscriberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subscribers)
}

// Apply runs an op, persists it, broadcasts the resulting diff
// to every subscriber, and pushes the inverse onto the undo
// stack. The redo stack is cleared (a new op invalidates any
// pending redo).
func (s *Session) Apply(ctx context.Context, deps Deps, op Op) (Diff, error) {
	if s.closed.Load() {
		return Diff{}, ErrSessionClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	diff, err := op.Apply(ctx, deps)
	if err != nil {
		return Diff{}, err
	}
	inverse, err := op.Inverse(ctx, deps)
	if err != nil {
		// Apply succeeded but we can't compute the inverse —
		// log + skip pushing the entry. The op is still
		// broadcast; only undo support degrades.
		// (Concrete ops should always be able to compute an
		// inverse, so this is a conservative path.)
		s.broadcastLocked(diff)
		return diff, nil
	}
	s.undoStack = append(s.undoStack, inverse)
	if len(s.undoStack) > undoLimit {
		s.undoStack = s.undoStack[1:]
	}
	s.redoStack = nil
	undoD := uint32(len(s.undoStack))
	redoD := uint32(len(s.redoStack))
	diff.UndoDepth = undoD
	diff.RedoDepth = redoD

	s.broadcastLocked(diff)
	if mdop, ok := op.(MultiDiffOp); ok {
		for _, ed := range mdop.ExtraDiffs() {
			ed.UndoDepth = undoD
			ed.RedoDepth = redoD
			s.broadcastLocked(ed)
		}
	}
	return diff, nil
}

// Undo pops the top of the undo stack, applies it, pushes its
// inverse onto the redo stack. Returns the diff that was applied.
func (s *Session) Undo(ctx context.Context, deps Deps) (Diff, error) {
	if s.closed.Load() {
		return Diff{}, ErrSessionClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.undoStack) == 0 {
		// Nothing to undo. Emit a HistoryChanged diff anyway so
		// the toolbar disabled state stays accurate (subscribers
		// that desynced their stack depth resync).
		d := Diff{Kind: DiffHistoryChanged, UndoDepth: 0, RedoDepth: uint32(len(s.redoStack))}
		s.broadcastLocked(d)
		return d, nil
	}
	op := s.undoStack[len(s.undoStack)-1]
	s.undoStack = s.undoStack[:len(s.undoStack)-1]
	diff, err := op.Apply(ctx, deps)
	if err != nil {
		// Failure to apply: push back so the user can retry.
		s.undoStack = append(s.undoStack, op)
		return Diff{}, err
	}
	if inv, err := op.Inverse(ctx, deps); err == nil {
		s.redoStack = append(s.redoStack, inv)
	}
	undoD := uint32(len(s.undoStack))
	redoD := uint32(len(s.redoStack))
	diff.UndoDepth = undoD
	diff.RedoDepth = redoD
	s.broadcastLocked(diff)
	if mdop, ok := op.(MultiDiffOp); ok {
		for _, ed := range mdop.ExtraDiffs() {
			ed.UndoDepth = undoD
			ed.RedoDepth = redoD
			s.broadcastLocked(ed)
		}
	}
	return diff, nil
}

// Redo is the symmetric counterpart of Undo.
func (s *Session) Redo(ctx context.Context, deps Deps) (Diff, error) {
	if s.closed.Load() {
		return Diff{}, ErrSessionClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.redoStack) == 0 {
		d := Diff{Kind: DiffHistoryChanged, UndoDepth: uint32(len(s.undoStack)), RedoDepth: 0}
		s.broadcastLocked(d)
		return d, nil
	}
	op := s.redoStack[len(s.redoStack)-1]
	s.redoStack = s.redoStack[:len(s.redoStack)-1]
	diff, err := op.Apply(ctx, deps)
	if err != nil {
		s.redoStack = append(s.redoStack, op)
		return Diff{}, err
	}
	if inv, err := op.Inverse(ctx, deps); err == nil {
		s.undoStack = append(s.undoStack, inv)
	}
	undoD := uint32(len(s.undoStack))
	redoD := uint32(len(s.redoStack))
	diff.UndoDepth = undoD
	diff.RedoDepth = redoD
	s.broadcastLocked(diff)
	if mdop, ok := op.(MultiDiffOp); ok {
		for _, ed := range mdop.ExtraDiffs() {
			ed.UndoDepth = undoD
			ed.RedoDepth = redoD
			s.broadcastLocked(ed)
		}
	}
	return diff, nil
}

// HistoryDepths returns the current (undo, redo) depths. Used by
// snapshot builders so a freshly-joining client sees the right
// toolbar state.
func (s *Session) HistoryDepths() (undo, redo uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return uint32(len(s.undoStack)), uint32(len(s.redoStack))
}

// Close marks the session torn-down. Subsequent ops error;
// subscribers see no further diffs. Idempotent.
func (s *Session) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers = nil
}

// broadcastLocked sends the diff to every subscriber. Caller
// holds s.mu. Non-blocking: a full channel drops the diff. WS
// handlers should use a comfortably-sized buffer (~64 messages).
func (s *Session) broadcastLocked(d Diff) {
	for _, sink := range s.subscribers {
		select {
		case sink <- d:
		default:
			// Drop. The subscriber's channel is full; their
			// diff stream is desynced. v1 leaves recovery to
			// the client (refresh = re-snapshot); future work
			// can wire automatic resync here.
		}
	}
}
