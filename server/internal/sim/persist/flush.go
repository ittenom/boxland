package persist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/sim/ecs"
)

// Persister coordinates Postgres + WAL for one level instance. Owns the
// flush + recovery flow.
type Persister struct {
	pool       *pgxpool.Pool
	wal        *WAL
	levelID    uint32
	instanceID string

	lastFlushedTick uint64
	lastFlushedID   string // Redis stream id; "0-0" means "before everything"
}

// NewPersister binds the database + WAL.
func NewPersister(pool *pgxpool.Pool, wal *WAL, levelID uint32, instanceID string) *Persister {
	return &Persister{
		pool:          pool,
		wal:           wal,
		levelID:       levelID,
		instanceID:    instanceID,
		lastFlushedID: "0-0",
	}
}

// Flush serializes the world into a MapState blob, upserts it into
// level_state, and trims the WAL up to the flushed tick. Marks the WAL
// "failing" on Postgres errors so subsequent Appends honor backpressure.
//
// Inputs are passed in (rather than the persister holding world refs)
// so the per-tick caller controls the sequencing.
func (p *Persister) Flush(ctx context.Context, in EncodeInputs) error {
	blob, err := EncodeMapState(in)
	if err != nil {
		p.wal.MarkFlushFailed()
		return fmt.Errorf("encode map state: %w", err)
	}

	// Find the last WAL stream id we should trim up to. We grab the
	// current XLEN-1th id BEFORE doing the flush so concurrent appends
	// during the flush don't get accidentally trimmed.
	muts, ids, err := p.wal.Range(ctx, p.lastFlushedID)
	if err != nil {
		p.wal.MarkFlushFailed()
		return fmt.Errorf("range wal: %w", err)
	}
	_ = muts // not actually replayed at flush time -- the in-memory world is the source of truth

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		p.wal.MarkFlushFailed()
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO level_state (level_id, instance_id, state_blob_fb, last_flushed_tick, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (level_id, instance_id) DO UPDATE
		SET state_blob_fb = EXCLUDED.state_blob_fb,
		    last_flushed_tick = EXCLUDED.last_flushed_tick,
		    updated_at = now()
	`, p.levelID, p.instanceID, blob, in.Tick); err != nil {
		p.wal.MarkFlushFailed()
		return fmt.Errorf("upsert level_state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		p.wal.MarkFlushFailed()
		return fmt.Errorf("commit: %w", err)
	}

	// Postgres flush is durable. Trim WAL last so a crash between commit
	// and trim is recoverable (recovery just replays mutations the snapshot
	// already covers; PLAN.md §4k explicitly accepts this).
	if len(ids) > 0 {
		newest := ids[len(ids)-1]
		if err := p.wal.Trim(ctx, newest); err != nil {
			// Trim failure isn't catastrophic; the durable state is committed.
			// Log + carry on; next flush will re-attempt.
			slog.Warn("wal trim", "err", err, "stream_key", p.wal.StreamKey())
		}
		p.lastFlushedID = newest
	}
	priorTick := p.lastFlushedTick
	p.lastFlushedTick = in.Tick
	p.wal.MarkFlushSucceeded()
	slog.Info("wal flush",
		"level_id", p.levelID,
		"instance_id", p.instanceID,
		"from_tick", priorTick,
		"to_tick", in.Tick,
		"mutations", len(ids),
	)
	return nil
}

// LastFlushedTick reports the most recent tick that hit Postgres.
func (p *Persister) LastFlushedTick() uint64 { return p.lastFlushedTick }

// Recover reads the canonical state from Postgres and replays any WAL
// entries newer than last_flushed_tick. Returns the replay count for
// telemetry and the current tick the runtime should resume from.
//
// Steps:
//   1. SELECT state_blob_fb, last_flushed_tick FROM level_state WHERE ...
//   2. Decode + ApplyMapState into the supplied world.
//   3. XRANGE the WAL from "0-0" forward; replay every Mutation whose
//      .Tick > last_flushed_tick into the world via the supplied applier.
//
// Returns ErrNoSnapshot if the (level_id, instance_id) has never been
// flushed; the caller can decide whether that's an empty fresh start
// (default) or a hard error (e.g. for a known-existed level).
type Applier func(m Mutation) error

// ErrNoSnapshot is returned by Recover when no level_state row exists.
var ErrNoSnapshot = errors.New("persist: no snapshot to recover")

// RecoveryResult bundles the outputs of one recovery pass.
type RecoveryResult struct {
	ReplayedMutations int
	ResumeTick        uint64
}

// Recover restores the world for a single instance:
//   1. Loads level_state.state_blob_fb + last_flushed_tick from Postgres.
//   2. Decodes + ApplyMapState(world, ms) -- entities + tiles re-spawn.
//   3. XRANGE the WAL forward; replays every Mutation whose .Tick exceeds
//      last_flushed_tick into the supplied applier.
//
// Returns ErrNoSnapshot if no level_state row exists; the caller decides
// whether that's a fresh-start (default) or a hard error.
func Recover(
	ctx context.Context,
	pool *pgxpool.Pool,
	wal *WAL,
	levelID uint32,
	instanceID string,
	world *ecs.World,
	applyMutation Applier,
) (RecoveryResult, error) {
	var blob []byte
	var lastFlushedTick uint64
	err := pool.QueryRow(ctx,
		`SELECT state_blob_fb, last_flushed_tick FROM level_state WHERE level_id = $1 AND instance_id = $2`,
		levelID, instanceID,
	).Scan(&blob, &lastFlushedTick)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RecoveryResult{}, ErrNoSnapshot
		}
		return RecoveryResult{}, fmt.Errorf("read level_state: %w", err)
	}

	ms, err := DecodeMapState(blob)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("decode level_state: %w", err)
	}
	ApplyMapState(world, ms)

	// Replay WAL entries newer than the flushed tick.
	muts, _, err := wal.Range(ctx, "0-0")
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("range wal: %w", err)
	}
	replayed := 0
	for _, m := range muts {
		if m.Tick <= lastFlushedTick {
			continue
		}
		if applyMutation != nil {
			if err := applyMutation(m); err != nil {
				return RecoveryResult{}, fmt.Errorf("apply mutation tick=%d seq=%d: %w", m.Tick, m.Seq, err)
			}
		}
		replayed++
	}
	resumeTick := lastFlushedTick
	if len(muts) > 0 && muts[len(muts)-1].Tick > resumeTick {
		resumeTick = muts[len(muts)-1].Tick
	}
	return RecoveryResult{ReplayedMutations: replayed, ResumeTick: resumeTick}, nil
}
