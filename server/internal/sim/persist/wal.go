// Boxland — Redis Streams WAL.
//
// One stream per (level_id, instance_id), keyed wal:level:{level}:{instance}.
// Each entry is a FlatBuffers-encoded Mutation. The single writer for a
// given instance is the goroutine that owns its tick loop; readers are
// the recovery boot path and (later) spectator/replay tooling.
//
// Flush policy: the runtime calls Flush every WALFlushTicks ticks
// (default 20 = ~2s at 10Hz). Flush:
//   1. Collects every WAL entry since the last flushed (tick, seq).
//   2. Encodes a fresh MapState blob from the in-memory world.
//   3. Upserts (level_state) + XTRIM MINID the stream to last-flushed in
//      a single coordinated step. Postgres tx + Redis trim are not in
//      one atomic transaction; we tolerate the brief overlap (a crash
//      between Postgres commit and XTRIM means recovery replays already-
//      committed mutations -- safe because mutations are idempotent at
//      the WAL replay layer; PLAN.md §4k).
//
// Safety bound: every Append uses XADD MAXLEN ~ 100000. If the bound is
// approached AND Postgres flush is failing, the runtime refuses new
// mutations (PLAN.md task #87) so we never silently drop history.
package persist

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/rueidis"
)

// WAL constants. Centralized so they can be tuned + asserted in tests.
const (
	WALFlushTicks    = 20      // every N ticks at 10 Hz (default ~2s)
	WALMaxLen        = 100_000 // ~2.7h of history at 10 Hz worst case
	walEntryFieldRaw = "raw"   // single field name on each XADD entry
)

// ErrWALFull is returned by Append when the stream length is approaching
// MAXLEN AND Postgres flush is failing (i.e., we cannot afford to drop
// older entries because they aren't durable yet).
var ErrWALFull = errors.New("persist: WAL near MAXLEN with unflushed entries")

// WAL is the per-instance write-ahead log.
type WAL struct {
	cli         rueidis.Client
	levelID     uint32
	instanceID  string
	streamKey   string

	// flushFailing flips true when the most recent flush returned an
	// error; reset on next successful flush. Append checks this together
	// with the stream length to honor the "refuse on near-full" rule.
	flushFailing bool
}

// NewWAL constructs a WAL bound to (levelID, instanceID).
func NewWAL(cli rueidis.Client, levelID uint32, instanceID string) *WAL {
	return &WAL{
		cli:        cli,
		levelID:    levelID,
		instanceID: instanceID,
		streamKey:  fmt.Sprintf("wal:level:%d:%s", levelID, instanceID),
	}
}

// StreamKey returns the Redis stream key. Useful for debug logs.
func (w *WAL) StreamKey() string { return w.streamKey }

// Append writes one Mutation to the stream. Honors the "refuse if WAL
// near full and flush failing" backpressure rule (PLAN.md task #87).
func (w *WAL) Append(ctx context.Context, m Mutation) error {
	if w.flushFailing {
		// Check current length; if close to MAXLEN, refuse.
		length, err := w.length(ctx)
		if err == nil && length >= WALMaxLen-10 {
			return ErrWALFull
		}
	}
	blob, err := EncodeMutation(m)
	if err != nil {
		return fmt.Errorf("encode mutation: %w", err)
	}
	cmd := w.cli.B().Xadd().
		Key(w.streamKey).
		Maxlen().Almost().Threshold(strconv.Itoa(WALMaxLen)).
		Id("*").
		FieldValue().FieldValue(walEntryFieldRaw, rueidis.BinaryString(blob)).
		Build()
	if err := w.cli.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("xadd: %w", err)
	}
	return nil
}

// Length returns the current stream length. Cheap; one XLEN.
func (w *WAL) Length(ctx context.Context) (int64, error) {
	return w.length(ctx)
}

// Range returns every mutation in the stream from `fromID` (exclusive)
// to the end. Recovery passes "0-0" to get everything; the flush path
// passes the last-flushed stream id to get just the unflushed tail.
func (w *WAL) Range(ctx context.Context, fromID string) ([]Mutation, []string, error) {
	exclusive := "(" + fromID
	cmd := w.cli.B().Xrange().Key(w.streamKey).Start(exclusive).End("+").Build()
	resp := w.cli.Do(ctx, cmd)
	if err := resp.Error(); err != nil {
		return nil, nil, fmt.Errorf("xrange: %w", err)
	}
	entries, err := resp.AsXRange()
	if err != nil {
		return nil, nil, fmt.Errorf("decode xrange: %w", err)
	}
	muts := make([]Mutation, 0, len(entries))
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		raw, ok := e.FieldValues[walEntryFieldRaw]
		if !ok {
			continue
		}
		mut, err := DecodeMutation([]byte(raw))
		if err != nil {
			return nil, nil, fmt.Errorf("decode entry %s: %w", e.ID, err)
		}
		muts = append(muts, mut)
		ids = append(ids, e.ID)
	}
	return muts, ids, nil
}

// Trim drops every entry whose stream id is <= `upToID`. Called after a
// successful Postgres flush so the WAL only retains unflushed history.
func (w *WAL) Trim(ctx context.Context, upToID string) error {
	cmd := w.cli.B().Xtrim().Key(w.streamKey).Minid().Threshold(upToID).Build()
	if err := w.cli.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("xtrim: %w", err)
	}
	return nil
}

// MarkFlushSucceeded clears the "flush failing" backpressure flag.
func (w *WAL) MarkFlushSucceeded() { w.flushFailing = false }

// MarkFlushFailed sets the backpressure flag so future Append calls
// reject when the stream is near MAXLEN.
func (w *WAL) MarkFlushFailed() { w.flushFailing = true }

// FlushFailing exposes the current backpressure state. Tests + telemetry.
func (w *WAL) FlushFailing() bool { return w.flushFailing }

func (w *WAL) length(ctx context.Context) (int64, error) {
	cmd := w.cli.B().Xlen().Key(w.streamKey).Build()
	resp := w.cli.Do(ctx, cmd)
	if err := resp.Error(); err != nil {
		return 0, fmt.Errorf("xlen: %w", err)
	}
	n, err := resp.AsInt64()
	if err != nil {
		return 0, err
	}
	return n, nil
}
