package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const advisoryLockID int64 = 7358764766271105025

var migrationName = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.(up|down)\.sql$`)

type Migration struct {
	Version  int64
	Name     string
	UpSQL    string
	DownSQL  string
	Checksum string
}

type Result struct {
	Applied []int64 `json:"applied"`
	Current int64   `json:"current"`
}

type Status struct {
	Applied int   `json:"applied"`
	Pending int   `json:"pending"`
	Current int64 `json:"current"`
	Latest  int64 `json:"latest"`
	Ready   bool  `json:"ready"`
}

type Runner struct {
	pool       *pgxpool.Pool
	migrations []Migration
}

func New(pool *pgxpool.Pool, source fs.FS) (*Runner, error) {
	if pool == nil {
		return nil, errors.New("migration runner requires a PostgreSQL pool")
	}
	loaded, err := Load(source)
	if err != nil {
		return nil, err
	}
	return &Runner{pool: pool, migrations: loaded}, nil
}

func Load(source fs.FS) ([]Migration, error) {
	if source == nil {
		return nil, errors.New("migration filesystem cannot be nil")
	}
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}
	type pair struct {
		version int64
		name    string
		up      string
		down    string
	}
	pairs := make(map[int64]*pair)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := filepath.Base(entry.Name())
		matches := migrationName.FindStringSubmatch(filename)
		if matches == nil {
			if strings.HasSuffix(strings.ToLower(filename), ".sql") {
				return nil, fmt.Errorf("migration SQL file %q does not match NNNNNN_name.(up|down).sql", filename)
			}
			continue
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", matches[1], err)
		}
		body, err := fs.ReadFile(source, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		item := pairs[version]
		if item == nil {
			item = &pair{version: version, name: matches[2]}
			pairs[version] = item
		}
		if item.name != matches[2] {
			return nil, fmt.Errorf("migration version %06d has conflicting names", version)
		}
		switch matches[3] {
		case "up":
			if item.up != "" {
				return nil, fmt.Errorf("migration version %06d has duplicate up file", version)
			}
			item.up = string(body)
		case "down":
			if item.down != "" {
				return nil, fmt.Errorf("migration version %06d has duplicate down file", version)
			}
			item.down = string(body)
		}
	}
	versions := make([]int64, 0, len(pairs))
	for version := range pairs {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	loaded := make([]Migration, 0, len(versions))
	for index, version := range versions {
		item := pairs[version]
		if item.up == "" || item.down == "" {
			return nil, fmt.Errorf("migration %06d_%s requires both up and down files", version, item.name)
		}
		if strings.TrimSpace(item.up) == "" || strings.TrimSpace(item.down) == "" {
			return nil, fmt.Errorf("migration %06d_%s contains an empty SQL file", version, item.name)
		}
		if index > 0 && version <= versions[index-1] {
			return nil, errors.New("migration versions must be strictly increasing")
		}
		digest := sha256.Sum256([]byte(item.up))
		loaded = append(loaded, Migration{
			Version:  version,
			Name:     item.name,
			UpSQL:    item.up,
			DownSQL:  item.down,
			Checksum: hex.EncodeToString(digest[:]),
		})
	}
	if len(loaded) == 0 {
		return nil, errors.New("no SQL migrations found")
	}
	return loaded, nil
}

func (r *Runner) Up(ctx context.Context) (result Result, err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Result{}, fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = rollbackMigration(tx)
		}
	}()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockID); err != nil {
		return Result{}, fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	if err = ensureSchemaTable(ctx, tx); err != nil {
		return Result{}, err
	}
	applied, err := readApplied(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if err = r.validateApplied(applied); err != nil {
		return Result{}, err
	}
	for _, migration := range r.migrations {
		if _, exists := applied[migration.Version]; exists {
			result.Current = migration.Version
			continue
		}
		started := time.Now()
		if _, err = tx.Exec(ctx, migration.UpSQL); err != nil {
			return result, fmt.Errorf("apply migration %06d_%s: %w", migration.Version, migration.Name, err)
		}
		elapsed := time.Since(started).Milliseconds()
		if _, err = tx.Exec(ctx, `
			INSERT INTO schema_migrations (version, name, checksum, execution_time_ms)
			VALUES ($1, $2, $3, $4)
		`, migration.Version, migration.Name, migration.Checksum, elapsed); err != nil {
			return result, fmt.Errorf("record migration %06d_%s: %w", migration.Version, migration.Name, err)
		}
		result.Applied = append(result.Applied, migration.Version)
		result.Current = migration.Version
	}
	if err = tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit migration transaction: %w", err)
	}
	return result, nil
}

func (r *Runner) Status(ctx context.Context) (Status, error) {
	exists, err := schemaTableExists(ctx, r.pool)
	if err != nil {
		return Status{}, err
	}
	status := Status{Latest: r.migrations[len(r.migrations)-1].Version}
	if !exists {
		status.Pending = len(r.migrations)
		return status, nil
	}
	applied, err := readApplied(ctx, r.pool)
	if err != nil {
		return Status{}, err
	}
	if err := r.validateApplied(applied); err != nil {
		return Status{}, err
	}
	status.Applied = len(applied)
	status.Pending = len(r.migrations) - len(applied)
	for version := range applied {
		if version > status.Current {
			status.Current = version
		}
	}
	status.Ready = status.Pending == 0 && status.Current == status.Latest
	return status, nil
}

// ErrStructuralSchema marks a schema state that retrying cannot resolve.
//
// A structural mismatch means the binary and the database disagree about
// what the schema *is* -- the database carries a migration this binary has
// never heard of, or a migration it knows by a different name or contents.
// That is a deployment fact, not a transient condition: the same comparison
// will fail identically forever until a different binary is deployed or the
// database is corrected. Callers must treat it differently from an
// unreachable database, which retrying does fix.
var ErrStructuralSchema = errors.New("structural schema mismatch between binary and database")

// validateApplied compares every migration the database has recorded against
// the set compiled into this binary.
//
// It reports the complete, sorted set of discrepancies rather than the first
// one encountered. The previous implementation ranged directly over the
// applied map, whose iteration order Go deliberately randomizes, so a single
// unchanging fault produced a different message on every retry -- observed in
// production alternating between versions 000035, 000037 and 000040 every
// five seconds, which actively obstructed diagnosis of one root cause.
func (r *Runner) validateApplied(applied map[int64]appliedMigration) error {
	known := make(map[int64]Migration, len(r.migrations))
	for _, migration := range r.migrations {
		known[migration.Version] = migration
	}
	versions := make([]int64, 0, len(applied))
	for version := range applied {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	var unknown, renamed, rechecksummed []string
	for _, version := range versions {
		row := applied[version]
		migration, ok := known[version]
		if !ok {
			unknown = append(unknown, fmt.Sprintf("%06d_%s", version, row.Name))
			continue
		}
		if row.Name != migration.Name {
			renamed = append(renamed, fmt.Sprintf("%06d database=%q source=%q", version, row.Name, migration.Name))
			continue
		}
		if row.Checksum != migration.Checksum {
			rechecksummed = append(rechecksummed, fmt.Sprintf("%06d_%s", version, migration.Name))
		}
	}
	if len(unknown) == 0 && len(renamed) == 0 && len(rechecksummed) == 0 {
		return nil
	}

	var detail []string
	if len(unknown) > 0 {
		detail = append(detail, fmt.Sprintf("database contains %d migration(s) unknown to this binary: %s", len(unknown), strings.Join(unknown, ", ")))
	}
	if len(renamed) > 0 {
		detail = append(detail, fmt.Sprintf("%d migration(s) renamed: %s", len(renamed), strings.Join(renamed, ", ")))
	}
	if len(rechecksummed) > 0 {
		detail = append(detail, fmt.Sprintf("%d migration(s) changed contents: %s", len(rechecksummed), strings.Join(rechecksummed, ", ")))
	}
	return fmt.Errorf("%w: binary knows up to %06d, database applied up to %06d; %s",
		ErrStructuralSchema, r.Tip(), versions[len(versions)-1], strings.Join(detail, "; "))
}

// Tip returns the highest migration version compiled into this binary.
// Comparing it against the database tip turns a deployment-drift incident
// into a two-number diff instead of an archaeology exercise.
func (r *Runner) Tip() int64 {
	var tip int64
	for _, migration := range r.migrations {
		if migration.Version > tip {
			tip = migration.Version
		}
	}
	return tip
}

type appliedMigration struct {
	Name     string
	Checksum string
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func ensureSchemaTable(ctx context.Context, db execer) error {
	_, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL CHECK (length(checksum) = 64),
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			execution_time_ms BIGINT NOT NULL CHECK (execution_time_ms >= 0)
		)
	`)
	if err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}
	return nil
}

func schemaTableExists(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check schema_migrations table: %w", err)
	}
	return exists, nil
}

func readApplied(ctx context.Context, db queryer) (map[int64]appliedMigration, error) {
	rows, err := db.Query(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	applied := make(map[int64]appliedMigration)
	for rows.Next() {
		var version int64
		var row appliedMigration
		if err := rows.Scan(&version, &row.Name, &row.Checksum); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

func rollbackMigration(tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tx.Rollback(ctx)
}
