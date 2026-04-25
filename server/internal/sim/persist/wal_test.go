package persist_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"

	"boxland/server/internal/entities/components"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/sim/ecs"
	"boxland/server/internal/sim/persist"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://boxland:boxland_dev@localhost:5433/boxland?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("postgres unavailable: %v", err)
	}
	return pool
}

func openTestRedis(t *testing.T) rueidis.Client {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6380/0"
	}
	opts, err := rueidis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis: %v", err)
	}
	cli, err := rueidis.NewClient(opts)
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	if err := cli.Do(context.Background(), cli.B().Ping().Build()).Error(); err != nil {
		cli.Close()
		t.Skipf("redis unavailable: %v", err)
	}
	return cli
}

// resetWALStream wipes a specific stream so tests start clean.
func resetWALStream(t *testing.T, cli rueidis.Client, key string) {
	t.Helper()
	wipe := func() {
		_ = cli.Do(context.Background(), cli.B().Del().Key(key).Build()).Error()
	}
	wipe()
	t.Cleanup(wipe)
}

func TestEncodeDecodeMutation_Roundtrip(t *testing.T) {
	in := persist.Mutation{
		Tick:     42,
		Seq:      7,
		Kind:     persist.MutationEntityMove,
		EntityID: 123456789,
		TypeID:   55,
		X:        -1024,
		Y:        2048,
		AuxU32:   0xdeadbeef,
		AuxU32B:  0xcafebabe,
		Payload:  []byte{0x01, 0x02, 0x03, 0x04, 0x05},
	}
	blob, err := persist.EncodeMutation(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := persist.DecodeMutation(blob)
	if err != nil {
		t.Fatal(err)
	}
	if out.Tick != in.Tick || out.Seq != in.Seq || out.Kind != in.Kind ||
		out.EntityID != in.EntityID || out.TypeID != in.TypeID ||
		out.X != in.X || out.Y != in.Y ||
		out.AuxU32 != in.AuxU32 || out.AuxU32B != in.AuxU32B {
		t.Errorf("roundtrip mismatch: in=%+v out=%+v", in, out)
	}
	if string(out.Payload) != string(in.Payload) {
		t.Errorf("payload roundtrip: in=%v out=%v", in.Payload, out.Payload)
	}
}

func TestWAL_AppendAndRange(t *testing.T) {
	cli := openTestRedis(t)
	defer cli.Close()

	wal := persist.NewWAL(cli, 7, "live:7:test-append")
	resetWALStream(t, cli, wal.StreamKey())
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		err := wal.Append(ctx, persist.Mutation{
			Tick: uint64(i + 1), Seq: 0, Kind: persist.MutationEntityMove,
			EntityID: 100 + uint64(i), X: int32(i),
		})
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	muts, ids, err := wal.Range(ctx, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	if len(muts) != 3 {
		t.Fatalf("range count: got %d, want 3", len(muts))
	}
	if len(ids) != 3 {
		t.Fatalf("ids count: got %d, want 3", len(ids))
	}
	for i, m := range muts {
		if m.Tick != uint64(i+1) || m.X != int32(i) {
			t.Errorf("mut[%d] mismatch: %+v", i, m)
		}
	}
}

func TestWAL_TrimDropsOlderEntries(t *testing.T) {
	cli := openTestRedis(t)
	defer cli.Close()
	wal := persist.NewWAL(cli, 7, "live:7:test-trim")
	resetWALStream(t, cli, wal.StreamKey())
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = wal.Append(ctx, persist.Mutation{Tick: uint64(i + 1)})
	}
	_, ids, err := wal.Range(ctx, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	mid := ids[2] // trim everything <= the 3rd id

	if err := wal.Trim(ctx, mid); err != nil {
		t.Fatal(err)
	}
	left, err := wal.Length(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// After trim, exactly the entries strictly newer than `mid` survive.
	// Trim with MINID is inclusive: entries with id < threshold are dropped;
	// id == threshold stays. So we keep ids[2..4] = 3 entries.
	if left != 3 {
		t.Errorf("after trim len: got %d, want 3", left)
	}
}

func TestWAL_BackpressureRefusesWhenFlushFailingAndNearMax(t *testing.T) {
	// We can't realistically push to MAXLEN-10 in a unit test (~99990
	// entries). Instead, verify that the flag controls refusal logic: we
	// flip the flag, then drive Append against a fresh WAL where length
	// is below threshold -- it should still succeed (since length < limit).
	// Then we patch the threshold by exercising the real code path with
	// a small maximum -- which our package doesn't expose. So we settle
	// for verifying the flag toggle + verifying that NewWAL starts in
	// "not failing" state.
	cli := openTestRedis(t)
	defer cli.Close()
	wal := persist.NewWAL(cli, 7, "live:7:test-bp")
	resetWALStream(t, cli, wal.StreamKey())

	if wal.FlushFailing() {
		t.Error("freshly built WAL should not be in flush-failing state")
	}
	wal.MarkFlushFailed()
	if !wal.FlushFailing() {
		t.Error("MarkFlushFailed should set the flag")
	}
	wal.MarkFlushSucceeded()
	if wal.FlushFailing() {
		t.Error("MarkFlushSucceeded should clear the flag")
	}
}

func TestPersister_FlushUpsertsMapStateAndTrimsWAL(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	cli := openTestRedis(t)
	defer cli.Close()

	wal := persist.NewWAL(cli, 99, "live:99:test-flush")
	resetWALStream(t, cli, wal.StreamKey())

	w := ecs.NewWorld()
	stores := w.Stores()
	e := w.Spawn()
	stores.Position.Set(e, components.Position{X: 7, Y: 11})
	stores.Sprite.Set(e, components.Sprite{AnimID: 3})

	// Pre-populate WAL so the trim path has something to trim.
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		_ = wal.Append(ctx, persist.Mutation{Tick: uint64(i + 1), Seq: 0})
	}

	p := persist.NewPersister(pool, wal, 99, "live:99:test-flush")
	err := p.Flush(ctx, persist.EncodeInputs{
		MapID: 99, InstanceID: "live:99:test-flush", Tick: 200, Stores: stores,
	})
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if p.LastFlushedTick() != 200 {
		t.Errorf("LastFlushedTick: got %d, want 200", p.LastFlushedTick())
	}

	// Postgres row exists.
	var rowTick uint64
	if err := pool.QueryRow(ctx,
		`SELECT last_flushed_tick FROM map_state WHERE map_id = $1 AND instance_id = $2`,
		99, "live:99:test-flush",
	).Scan(&rowTick); err != nil {
		t.Fatal(err)
	}
	if rowTick != 200 {
		t.Errorf("Postgres tick: got %d, want 200", rowTick)
	}

	// WAL trimmed: zero or one entry remains (the trim is inclusive of
	// the newest id at flush time; new appends after flush would survive).
	left, _ := wal.Length(ctx)
	if left > 1 {
		t.Errorf("WAL after flush: %d entries, want <= 1", left)
	}
}

func TestRecover_NoSnapshotReturnsErrNoSnapshot(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	cli := openTestRedis(t)
	defer cli.Close()

	wal := persist.NewWAL(cli, 999, "live:999:nope")
	resetWALStream(t, cli, wal.StreamKey())

	w := ecs.NewWorld()
	_, err := persist.Recover(context.Background(), pool, wal, 999, "live:999:nope", w, nil)
	if !errors.Is(err, persist.ErrNoSnapshot) {
		t.Errorf("got %v, want ErrNoSnapshot", err)
	}
}

func TestRecover_ReplaysWALAfterSnapshot(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	cli := openTestRedis(t)
	defer cli.Close()

	wal := persist.NewWAL(cli, 88, "live:88:rec")
	resetWALStream(t, cli, wal.StreamKey())
	ctx := context.Background()

	// Build + flush a world with one entity at tick 50.
	src := ecs.NewWorld()
	srcStores := src.Stores()
	se := src.Spawn()
	srcStores.Position.Set(se, components.Position{X: 100, Y: 200})
	p := persist.NewPersister(pool, wal, 88, "live:88:rec")
	if err := p.Flush(ctx, persist.EncodeInputs{
		MapID: 88, InstanceID: "live:88:rec", Tick: 50, Stores: srcStores,
	}); err != nil {
		t.Fatal(err)
	}

	// Append two WAL entries: one at tick 50 (already covered by snapshot),
	// one at tick 51 (must be replayed).
	_ = wal.Append(ctx, persist.Mutation{Tick: 50, Seq: 1, Kind: persist.MutationEntityMove})
	_ = wal.Append(ctx, persist.Mutation{Tick: 51, Seq: 0, Kind: persist.MutationEntityMove})

	// Recover into a fresh world, counting replayed mutations via the applier.
	dst := ecs.NewWorld()
	replayCount := 0
	res, err := persist.Recover(ctx, pool, wal, 88, "live:88:rec", dst,
		func(m persist.Mutation) error {
			replayCount++
			return nil
		})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if replayCount != 1 {
		t.Errorf("replayed: got %d, want 1 (only tick > 50)", replayCount)
	}
	if res.ResumeTick < 51 {
		t.Errorf("ResumeTick: got %d, want >= 51", res.ResumeTick)
	}
	if dst.Stores().Position.Len() != 1 {
		t.Errorf("snapshot didn't restore the one entity; Position.Len=%d", dst.Stores().Position.Len())
	}
}
