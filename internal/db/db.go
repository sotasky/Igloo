package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/storage"

	_ "modernc.org/sqlite"
)

type PhaseFunc func(name string, elapsed time.Duration)

type OpenOptions struct {
	Phase PhaseFunc
}

type EnsureSchemaOptions struct {
	Phase PhaseFunc
}

type DB struct {
	conn                 *sql.DB
	readTx               *sql.Tx
	mu                   sync.Mutex // serialize writes
	storage              storage.Layout
	readyAssetDurability func(string) error
}

type sqlReader interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (db *DB) reader() sqlReader {
	if db.readTx != nil {
		return db.readTx
	}
	return db.conn
}

func (db *DB) WithReadSnapshot(fn func(*DB) error) error {
	tx, err := db.conn.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	snapshot := &DB{
		conn: db.conn, readTx: tx, storage: db.storage,
		readyAssetDurability: db.readyAssetDurability,
	}
	if err := fn(snapshot); err != nil {
		return err
	}
	return tx.Commit()
}

// Open opens the database for read-write with WAL mode.
func Open(layout storage.Layout) (*DB, error) {
	return OpenWithOptions(layout, OpenOptions{})
}

// OpenWithOptions opens the database for read-write with WAL mode and optional
// startup phase reporting.
func OpenWithOptions(layout storage.Layout, opts OpenOptions) (*DB, error) {
	if err := layout.Ensure(); err != nil {
		return nil, fmt.Errorf("validate storage layout: %w", err)
	}
	return openPathWithOptions(layout.DatabasePath(), layout, opts)
}

// OpenPath opens an explicit database copy with co-located local storage.
// Runtime callers use Open; this boundary exists for maintenance tools and
// schema or restore tests that intentionally operate on another database file.
func OpenPath(path, stateRoot string) (*DB, error) {
	layout, err := storage.New(stateRoot, "")
	if err != nil {
		return nil, err
	}
	return openPathWithOptions(path, layout, OpenOptions{})
}

func OpenLayoutPath(path string, layout storage.Layout) (*DB, error) {
	if err := layout.Ensure(); err != nil {
		return nil, fmt.Errorf("validate storage layout: %w", err)
	}
	return openPathWithOptions(path, layout, OpenOptions{})
}

func openPathWithOptions(path string, layout storage.Layout, opts OpenOptions) (*DB, error) {
	totalStart := time.Now()

	phaseStart := time.Now()
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)&_pragma=busy_timeout(30000)&_pragma=foreign_keys(on)", path)
	conn, err := sql.Open("sqlite", dsn)
	reportPhase(opts.Phase, "db.sql_open", phaseStart)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	phaseStart = time.Now()
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		reportPhase(opts.Phase, "db.ping", phaseStart)
		return nil, fmt.Errorf("ping db: %w", err)
	}
	reportPhase(opts.Phase, "db.ping", phaseStart)

	d := &DB{conn: conn, storage: layout}
	phaseStart = time.Now()
	present, err := schemaPresent(conn)
	if err == nil && present {
		err = ValidateCurrentSchema(conn)
	} else if err == nil {
		err = EnsureSchemaWithOptions(conn, EnsureSchemaOptions(opts))
	}
	if err != nil {
		_ = conn.Close()
		reportPhase(opts.Phase, "db.ensure_schema", phaseStart)
		return nil, fmt.Errorf("ensure schema: %w", err)
	}
	reportPhase(opts.Phase, "db.ensure_schema", phaseStart)

	reportPhase(opts.Phase, "db.open_total", totalStart)
	return d, nil
}

// OpenReadOnly opens the database in read-only mode.
func OpenReadOnly(path, stateRoot string) (*DB, error) {
	layout, err := storage.New(stateRoot, "")
	if err != nil {
		return nil, err
	}
	return OpenReadOnlyLayout(path, layout)
}

func OpenReadOnlyLayout(path string, layout storage.Layout) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(30000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db readonly: %w", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return &DB{conn: conn, storage: layout}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// WithRead executes a read-only function against the database.
// No lock needed — WAL allows concurrent reads.
func (db *DB) WithRead(fn func(conn *sql.DB) error) error {
	if db.readTx != nil {
		return fmt.Errorf("direct connection read unavailable inside read snapshot")
	}
	return fn(db.conn)
}

// ExecRaw exposes raw SQL execution (used by tests).
func (db *DB) ExecRaw(query string, args ...any) error {
	_, err := db.conn.Exec(query, args...)
	return err
}

// VacuumInto creates a consistent snapshot of the database at dstPath.
func (db *DB) VacuumInto(ctx context.Context, dstPath string) error {
	_, err := db.conn.ExecContext(ctx, `VACUUM INTO ?`, dstPath)
	return err
}

// QueryRow exposes raw single-row queries (used by tests).
func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.reader().QueryRow(query, args...)
}

// WithWrite executes a write function inside a transaction with mutex.
func (db *DB) WithWrite(fn func(tx *sql.Tx) error) error {
	if db.readTx != nil {
		return fmt.Errorf("write transaction unavailable inside read snapshot")
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func reportPhase(cb PhaseFunc, name string, started time.Time) {
	if cb != nil {
		cb(name, time.Since(started))
	}
}
