// Package repo provides a small generic CRUD layer for design-tool surfaces
// (assets, entity types, maps, palettes, etc.) on top of pgx + squirrel.
//
// Hot paths (tick loop, AOI loads, WAL flush) bypass this and use sqlc-
// generated queries instead. See PLAN.md §1 "Postgres access pattern".
//
// Tag conventions on row structs:
//
//	`db:"col_name"`      column name; required for repo-managed fields
//	`pk:"auto"`          marks the auto-generated primary key
//	`repo:"readonly"`    column is populated by the DB (e.g. created_at);
//	                     excluded from INSERT and UPDATE, but included in
//	                     RETURNING so the struct is refreshed on Insert
//
// Reflection happens once per (T, table) at New(); per-call paths use the
// cached column lists with no further reflection.
package repo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by Get when no row matches the id.
// Wrap-friendly so callers can use errors.Is or repo.IsNotFound.
var ErrNotFound = errors.New("repo: not found")

// IsNotFound reports whether err (or a wrapped error in its chain) is ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// ListOpts controls a List call. Where accepts any squirrel.Sqlizer
// (squirrel.Eq{}, squirrel.Gt{}, And{}, custom Sqlizer, etc.).
type ListOpts struct {
	Where  squirrel.Sqlizer
	Order  string // raw, e.g. "created_at DESC, id ASC"
	Limit  uint64
	Offset uint64
}

// Repo is a typed CRUD repository for table T. Construct with New.
type Repo[T any] struct {
	pool   *pgxpool.Pool
	table  string
	cols   columnInfo
	psql   squirrel.StatementBuilderType
}

// New builds a Repo[T] for the given table. Panics on misconfigured row
// structs (missing pk, missing db tags) so the bug surfaces at boot time
// rather than on the first request.
func New[T any](pool *pgxpool.Pool, table string) *Repo[T] {
	var zero T
	cols, err := inspect(reflect.TypeOf(zero))
	if err != nil {
		panic(fmt.Sprintf("repo.New[%T]: %v", zero, err))
	}
	return &Repo[T]{
		pool:  pool,
		table: table,
		cols:  cols,
		psql:  squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}
}

// Get returns the row whose primary key equals id, or ErrNotFound.
func (r *Repo[T]) Get(ctx context.Context, id any) (*T, error) {
	q, args, err := r.psql.
		Select(r.cols.allColumnsSelect()...).
		From(r.table).
		Where(squirrel.Eq{r.cols.pkColumn: id}).
		Limit(1).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("repo.Get build: %w", err)
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("repo.Get query: %w", err)
	}
	defer rows.Close()
	row, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[T])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repo.Get scan: %w", err)
	}
	return row, nil
}

// List returns all rows matching opts. Without filters, opts can be the zero value.
func (r *Repo[T]) List(ctx context.Context, opts ListOpts) ([]T, error) {
	q := r.psql.Select(r.cols.allColumnsSelect()...).From(r.table)
	if opts.Where != nil {
		q = q.Where(opts.Where)
	}
	if opts.Order != "" {
		q = q.OrderBy(opts.Order)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	sql, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("repo.List build: %w", err)
	}
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("repo.List query: %w", err)
	}
	defer rows.Close()
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if err != nil {
		return nil, fmt.Errorf("repo.List scan: %w", err)
	}
	return out, nil
}

// Insert inserts row and refreshes it from the DB (including auto-generated
// PK and any readonly columns) via RETURNING *.
func (r *Repo[T]) Insert(ctx context.Context, row *T) error {
	v := reflect.ValueOf(row).Elem()
	values := make([]any, 0, len(r.cols.insertColumns))
	for _, fi := range r.cols.insertFields {
		values = append(values, v.Field(fi).Interface())
	}
	sql, args, err := r.psql.
		Insert(r.table).
		Columns(r.cols.insertColumns...).
		Values(values...).
		Suffix("RETURNING " + strings.Join(r.cols.allColumnsSelect(), ", ")).
		ToSql()
	if err != nil {
		return fmt.Errorf("repo.Insert build: %w", err)
	}
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("repo.Insert query: %w", err)
	}
	defer rows.Close()
	refreshed, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[T])
	if err != nil {
		return fmt.Errorf("repo.Insert scan: %w", err)
	}
	*row = *refreshed
	return nil
}

// Update writes all non-pk, non-readonly columns of row back to the DB,
// keyed by row's primary key. Returns ErrNotFound if no row matched.
func (r *Repo[T]) Update(ctx context.Context, row *T) error {
	v := reflect.ValueOf(row).Elem()
	pkVal := v.Field(r.cols.pkField).Interface()

	setMap := make(map[string]any, len(r.cols.updateColumns))
	for i, col := range r.cols.updateColumns {
		setMap[col] = v.Field(r.cols.updateFields[i]).Interface()
	}
	sql, args, err := r.psql.
		Update(r.table).
		SetMap(setMap).
		Where(squirrel.Eq{r.cols.pkColumn: pkVal}).
		ToSql()
	if err != nil {
		return fmt.Errorf("repo.Update build: %w", err)
	}
	tag, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("repo.Update exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the row whose pk equals id. Returns ErrNotFound if no
// row was deleted.
func (r *Repo[T]) Delete(ctx context.Context, id any) error {
	sql, args, err := r.psql.
		Delete(r.table).
		Where(squirrel.Eq{r.cols.pkColumn: id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("repo.Delete build: %w", err)
	}
	tag, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("repo.Delete exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- internals: one-time reflection ----

type columnInfo struct {
	pkField       int      // index into struct fields
	pkColumn      string   // db column name of the pk
	allColumns    []string // every db column, in struct order
	insertColumns []string // columns supplied on INSERT (excludes pk-auto and readonly)
	insertFields  []int    // matching struct field indices for insertColumns
	updateColumns []string // columns supplied on UPDATE (excludes pk and readonly)
	updateFields  []int    // matching struct field indices for updateColumns
}

func (c columnInfo) allColumnsSelect() []string {
	// Defensive copy so callers can't mutate the cached slice.
	out := make([]string, len(c.allColumns))
	copy(out, c.allColumns)
	return out
}

func inspect(t reflect.Type) (columnInfo, error) {
	if t.Kind() != reflect.Struct {
		return columnInfo{}, fmt.Errorf("row type must be a struct, got %s", t.Kind())
	}
	info := columnInfo{pkField: -1}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		col, ok := f.Tag.Lookup("db")
		if !ok || col == "-" {
			continue
		}
		info.allColumns = append(info.allColumns, col)

		isPK := f.Tag.Get("pk") != ""
		isAuto := f.Tag.Get("pk") == "auto"
		isReadonly := f.Tag.Get("repo") == "readonly"

		if isPK {
			if info.pkField != -1 {
				return columnInfo{}, fmt.Errorf("multiple pk fields on %s (%q and %q)",
					t.Name(), info.pkColumn, col)
			}
			info.pkField = i
			info.pkColumn = col
		}

		// INSERT skips auto-pk and readonly columns; non-auto pk values are
		// still inserted (callers may want to specify their own ids).
		if !isReadonly && !isAuto {
			info.insertColumns = append(info.insertColumns, col)
			info.insertFields = append(info.insertFields, i)
		}

		// UPDATE skips pk (used for WHERE) and readonly columns.
		if !isReadonly && !isPK {
			info.updateColumns = append(info.updateColumns, col)
			info.updateFields = append(info.updateFields, i)
		}
	}
	if info.pkField == -1 {
		return columnInfo{}, fmt.Errorf("%s has no field tagged pk:\"…\"", t.Name())
	}
	return info, nil
}
