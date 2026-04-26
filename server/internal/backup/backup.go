package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"boxland/server/internal/config"
	"boxland/server/internal/persistence"
	"boxland/server/internal/sim/persist"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"
)

const Format = "boxland.backup.v1"

type Manifest struct {
	Format         string           `json:"format"`
	CreatedAt      time.Time        `json:"created_at"`
	BoxlandVersion string           `json:"boxland_version"`
	Migration      MigrationVersion `json:"migration"`
	Includes       map[string]bool  `json:"includes"`
}

type MigrationVersion struct {
	Version uint `json:"version"`
	Dirty   bool `json:"dirty"`
}

type Options struct {
	Version string
	Logger  *slog.Logger
}

type RedisStream struct {
	Key     string       `json:"key"`
	Entries []RedisEntry `json:"entries"`
}

type RedisEntry struct {
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields"`
}

func Export(ctx context.Context, cfg config.Config, dst string, opt Options) error {
	if dst == "" {
		return errors.New("backup export path is required")
	}
	log := logger(opt)
	tmp, err := os.MkdirTemp("", "boxland-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	pool, err := persistence.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	version, dirty, err := persistence.MigrateVersion(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("migration version: %w", err)
	}
	if dirty {
		return errors.New("database migration is dirty; refusing to export")
	}
	log.Info("exporting postgres")
	if err := exportPostgres(ctx, pool, filepath.Join(tmp, "postgres.sql")); err != nil {
		return err
	}

	log.Info("exporting object store")
	objectsIncluded, err := exportObjects(ctx, cfg, filepath.Join(tmp, "objects"))
	if err != nil {
		return err
	}

	log.Info("exporting redis wal")
	redisIncluded, err := exportRedisWAL(ctx, cfg.RedisURL, filepath.Join(tmp, "redis-wal.json"))
	if err != nil {
		return err
	}

	manifest := Manifest{
		Format:         Format,
		CreatedAt:      time.Now().UTC(),
		BoxlandVersion: opt.Version,
		Migration:      MigrationVersion{Version: version, Dirty: dirty},
		Includes: map[string]bool{
			"postgres":  true,
			"objects":   objectsIncluded,
			"redis_wal": redisIncluded,
		},
	}
	if err := writeJSON(filepath.Join(tmp, "manifest.json"), manifest); err != nil {
		return err
	}
	if err := packTarGz(tmp, dst); err != nil {
		return err
	}
	log.Info("backup exported", "path", dst)
	return nil
}

func Import(ctx context.Context, cfg config.Config, src string, yes bool, opt Options) error {
	if src == "" {
		return errors.New("backup import path is required")
	}
	if !yes {
		return errors.New("restore is destructive; rerun with --yes")
	}
	log := logger(opt)
	tmp, err := os.MkdirTemp("", "boxland-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := unpackTarGz(src, tmp); err != nil {
		return err
	}
	var manifest Manifest
	if err := readJSON(filepath.Join(tmp, "manifest.json"), &manifest); err != nil {
		return err
	}
	if manifest.Format != Format {
		return fmt.Errorf("unsupported backup format %q", manifest.Format)
	}
	if manifest.Migration.Dirty {
		return errors.New("backup was created from a dirty migration state")
	}

	pool, err := persistence.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Info("restoring postgres")
	if err := importPostgres(ctx, pool, filepath.Join(tmp, "postgres.sql")); err != nil {
		return err
	}
	log.Info("restoring object store")
	if err := importObjects(ctx, cfg, filepath.Join(tmp, "objects")); err != nil {
		return err
	}
	log.Info("restoring redis wal")
	if err := importRedisWAL(ctx, cfg.RedisURL, filepath.Join(tmp, "redis-wal.json")); err != nil {
		return err
	}
	log.Info("backup restored", "path", src)
	return nil
}

func exportPostgres(ctx context.Context, pool *pgxpool.Pool, dst string) error {
	if path, err := exec.LookPath("pg_dump"); err == nil {
		if err := runCommand(ctx, path, "--dbname", connString(pool), "--file", dst, "--format", "plain", "--data-only", "--disable-triggers", "--no-owner", "--no-acl"); err == nil {
			return nil
		}
	}
	rows, err := pool.Query(ctx, `
		SELECT table_schema, table_name
		FROM information_schema.tables
		WHERE table_type = 'BASE TABLE' AND table_schema = 'public'
		ORDER BY table_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type table struct{ schema, name string }
	var tables []table
	for rows.Next() {
		var t table
		if err := rows.Scan(&t.schema, &t.name); err != nil {
			return err
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, _ = fmt.Fprintln(f, "BEGIN;")
	_, _ = fmt.Fprintln(f, "DROP SCHEMA IF EXISTS public CASCADE;")
	_, _ = fmt.Fprintln(f, "CREATE SCHEMA public;")
	_, _ = fmt.Fprintln(f, "COMMIT;")
	_, _ = fmt.Fprintln(f)
	for _, t := range tables {
		var name string
		var def any
		err := pool.QueryRow(ctx, `SELECT table_name, table_def FROM pg_dump_export WHERE table_schema=$1 AND table_name=$2`, t.schema, t.name).Scan(&name, &def)
		_ = err
	}
	// pg_dump-equivalent pure Go DDL is intentionally delegated to pg_dump when possible.
	// For this repo's migrations, restoring by rerunning migrations and COPYing data is safer than synthesizing DDL.
	_, _ = fmt.Fprintln(f, "-- Boxland data export. Restore runs migrations before COPYing data.")
	for _, t := range tables {
		cols, err := columns(ctx, pool, t.name)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(f, "COPY %s (%s) FROM stdin;\n", quoteIdent(t.name), joinQuoted(cols))
		copyRows, err := pool.Query(ctx, "SELECT "+joinQuoted(cols)+" FROM "+quoteIdent(t.name))
		if err != nil {
			return err
		}
		for copyRows.Next() {
			vals, err := copyRows.Values()
			if err != nil {
				copyRows.Close()
				return err
			}
			for i, v := range vals {
				if i > 0 {
					_, _ = fmt.Fprint(f, "\t")
				}
				_, _ = fmt.Fprint(f, pgCopyValue(v))
			}
			_, _ = fmt.Fprintln(f)
		}
		if err := copyRows.Err(); err != nil {
			copyRows.Close()
			return err
		}
		copyRows.Close()
		_, _ = fmt.Fprintln(f, "\\.")
		_, _ = fmt.Fprintln(f)
	}
	return nil
}

func importPostgres(ctx context.Context, pool *pgxpool.Pool, src string) error {
	dsn := connString(pool)
	// Reset schema and recreate it using embedded migrations, then feed COPY data.
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`); err != nil {
		return err
	}
	pool.Close()
	if err := persistence.MigrateUp(dsn); err != nil {
		return err
	}
	pool2, err := persistence.NewPool(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool2.Close()
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	parts := strings.Split(string(b), "-- Boxland data export. Restore runs migrations before COPYing data.")
	if len(parts) < 2 {
		return errors.New("postgres.sql missing data section")
	}
	_, err = pool2.Exec(ctx, parts[1])
	return err
}

func columns(ctx context.Context, pool *pgxpool.Pool, table string) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func exportObjects(ctx context.Context, cfg config.Config, dst string) (bool, error) {
	store, err := newS3(ctx, cfg)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return false, err
	}
	p := s3.NewListObjectsV2Paginator(store, &s3.ListObjectsV2Input{Bucket: aws.String(cfg.S3Bucket)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return false, err
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key == "" {
				continue
			}
			out, err := store.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(cfg.S3Bucket), Key: aws.String(key)})
			if err != nil {
				return false, err
			}
			path := filepath.Join(dst, filepath.FromSlash(key))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				out.Body.Close()
				return false, err
			}
			f, err := os.Create(path)
			if err != nil {
				out.Body.Close()
				return false, err
			}
			_, copyErr := io.Copy(f, out.Body)
			closeErr := out.Body.Close()
			fileErr := f.Close()
			if copyErr != nil {
				return false, copyErr
			}
			if closeErr != nil {
				return false, closeErr
			}
			if fileErr != nil {
				return false, fileErr
			}
		}
	}
	return true, nil
}

func importObjects(ctx context.Context, cfg config.Config, src string) error {
	store, err := newS3(ctx, cfg)
	if err != nil {
		return err
	}
	p := s3.NewListObjectsV2Paginator(store, &s3.ListObjectsV2Input{Bucket: aws.String(cfg.S3Bucket)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key != "" {
				_, err = store.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(cfg.S3Bucket), Key: aws.String(key)})
				if err != nil {
					return err
				}
			}
		}
	}
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		_, err = store.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(cfg.S3Bucket), Key: aws.String(key), Body: f, ContentLength: aws.Int64(info.Size())})
		return err
	})
}

func newS3(ctx context.Context, cfg config.Config) (*s3.Client, error) {
	obj, err := persistence.NewObjectStore(ctx, persistence.ObjectStoreConfig{Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket, AccessKeyID: cfg.S3AccessKeyID, SecretAccessKey: cfg.S3SecretAccessKey, UsePathStyle: cfg.S3UsePathStyle, PublicBaseURL: cfg.S3PublicBaseURL})
	if err != nil {
		return nil, err
	}
	return obj.S3Client(), nil
}

func exportRedisWAL(ctx context.Context, url, dst string) (bool, error) {
	cli, err := persistence.NewRedis(ctx, url)
	if err != nil {
		return false, err
	}
	defer cli.Close()
	var cursor uint64
	var streams []RedisStream
	for {
		cmd := cli.B().Scan().Cursor(cursor).Match("wal:map:*").Count(100).Build()
		resp := cli.Do(ctx, cmd)
		if err := resp.Error(); err != nil {
			return false, err
		}
		entry, err := resp.AsScanEntry()
		if err != nil {
			return false, err
		}
		cursor = entry.Cursor
		for _, key := range entry.Elements {
			entries, err := redisEntries(ctx, cli.Client, key)
			if err != nil {
				return false, err
			}
			streams = append(streams, RedisStream{Key: key, Entries: entries})
		}
		if cursor == 0 {
			break
		}
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].Key < streams[j].Key })
	return true, writeJSON(dst, streams)
}

func importRedisWAL(ctx context.Context, url, src string) error {
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	cli, err := persistence.NewRedis(ctx, url)
	if err != nil {
		return err
	}
	defer cli.Close()
	cmd := cli.B().Scan().Cursor(0).Match("wal:map:*").Count(1000).Build()
	resp := cli.Do(ctx, cmd)
	if err := resp.Error(); err != nil {
		return err
	}
	entry, err := resp.AsScanEntry()
	if err != nil {
		return err
	}
	for _, key := range entry.Elements {
		if err := cli.Do(ctx, cli.B().Del().Key(key).Build()).Error(); err != nil {
			return err
		}
	}
	var streams []RedisStream
	if err := readJSON(src, &streams); err != nil {
		return err
	}
	for _, st := range streams {
		for _, e := range st.Entries {
			fv := cli.B().Xadd().Key(st.Key).Id(e.ID).FieldValue()
			for k, v := range e.Fields {
				fv = fv.FieldValue(k, v)
			}
			if err := cli.Do(ctx, fv.Build()).Error(); err != nil {
				return err
			}
		}
	}
	return nil
}

func redisEntries(ctx context.Context, cli rueidis.Client, key string) ([]RedisEntry, error) {
	resp := cli.Do(ctx, cli.B().Xrange().Key(key).Start("-").End("+").Build())
	if err := resp.Error(); err != nil {
		return nil, err
	}
	xr, err := resp.AsXRange()
	if err != nil {
		return nil, err
	}
	entries := make([]RedisEntry, 0, len(xr))
	for _, x := range xr {
		fields := make(map[string]string, len(x.FieldValues))
		for k, v := range x.FieldValues {
			fields[k] = v
		}
		entries = append(entries, RedisEntry{ID: x.ID, Fields: fields})
	}
	return entries, nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func joinQuoted(cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = quoteIdent(c)
	}
	return strings.Join(out, ", ")
}
func connString(pool *pgxpool.Pool) string { return pool.Config().ConnString() }
func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func pgCopyValue(v any) string {
	if v == nil {
		return `\N`
	}
	s := fmt.Sprint(v)
	s = strings.ReplaceAll(s, `\\`, `\\\\`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func logger(opt Options) *slog.Logger {
	if opt.Logger != nil {
		return opt.Logger
	}
	return slog.Default()
}

func packTarGz(srcDir, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil && filepath.Dir(dst) != "." {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
}

func unpackTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(h.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("unsafe tar path %q", h.Name)
		}
		path := filepath.Join(dst, clean)
		if h.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, h.FileInfo().Mode())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// keep import for WAL constants package documentation while avoiding drift in backup scope.
var _ = persist.WALMaxLen
