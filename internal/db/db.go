package db

import (
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

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
	conn    *sql.DB
	mu      sync.Mutex // serialize writes
	dataDir string
	syncSeq atomic.Int64 // monotonic counter for feed_items.sync_seq
}

// Open opens the database for read-write with WAL mode.
func Open(path, dataDir string) (*DB, error) {
	return OpenWithOptions(path, dataDir, OpenOptions{})
}

// OpenWithOptions opens the database for read-write with WAL mode and optional
// startup phase reporting.
func OpenWithOptions(path, dataDir string, opts OpenOptions) (*DB, error) {
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

	d := &DB{conn: conn, dataDir: dataDir}
	phaseStart = time.Now()
	if err := EnsureSchemaWithOptions(conn, EnsureSchemaOptions(opts)); err != nil {
		_ = conn.Close()
		reportPhase(opts.Phase, "db.ensure_schema", phaseStart)
		return nil, fmt.Errorf("ensure schema: %w", err)
	}
	reportPhase(opts.Phase, "db.ensure_schema", phaseStart)

	phaseStart = time.Now()
	if err := d.cleanupRetiredReadingFeature(); err != nil {
		_ = conn.Close()
		reportPhase(opts.Phase, "db.cleanup_retired_reading", phaseStart)
		return nil, fmt.Errorf("cleanup retired reading feature: %w", err)
	}
	reportPhase(opts.Phase, "db.cleanup_retired_reading", phaseStart)

	phaseStart = time.Now()
	if err := d.initSyncSeq(); err != nil {
		_ = conn.Close()
		reportPhase(opts.Phase, "db.init_sync_seq", phaseStart)
		return nil, fmt.Errorf("init sync_seq: %w", err)
	}
	reportPhase(opts.Phase, "db.init_sync_seq", phaseStart)

	phaseStart = time.Now()
	if err := d.repairTwitterPlaceholderAuthorsOnce(); err != nil {
		_ = conn.Close()
		reportPhase(opts.Phase, "db.repair_twitter_placeholder_authors", phaseStart)
		return nil, fmt.Errorf("repair twitter placeholder authors: %w", err)
	}
	reportPhase(opts.Phase, "db.repair_twitter_placeholder_authors", phaseStart)

	phaseStart = time.Now()
	if err := d.RepairVideoMediaShapesOnce(); err != nil {
		_ = conn.Close()
		reportPhase(opts.Phase, "db.repair_video_media_shapes", phaseStart)
		return nil, fmt.Errorf("repair video media shapes: %w", err)
	}
	reportPhase(opts.Phase, "db.repair_video_media_shapes", phaseStart)

	reportPhase(opts.Phase, "db.open_total", totalStart)
	return d, nil
}

// OpenReadOnly opens the database in read-only mode.
func OpenReadOnly(path, dataDir string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=journal_mode(wal)&_pragma=busy_timeout(30000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db readonly: %w", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return &DB{conn: conn, dataDir: dataDir}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// initSyncSeq seeds the global sync_seq counter from the max value across
// every primary-row table that participates in bundle-delta endpoints (#6).
// All tables share one monotonic to keep cross-stream ordering
// deterministic when a single request touches multiple tables.
func (db *DB) initSyncSeq() error {
	var maxSeq int64
	err := db.conn.QueryRow(`
		SELECT MAX(seq) FROM (
			SELECT COALESCE(MAX(sync_seq), 0) AS seq FROM feed_items
			UNION ALL SELECT COALESCE(MAX(sync_seq), 0) FROM videos
			UNION ALL SELECT COALESCE(MAX(sync_seq), 0) FROM channels
		)`).Scan(&maxSeq)
	if err != nil {
		return err
	}
	db.syncSeq.Store(maxSeq)
	return nil
}

// NextSyncSeq atomically increments and returns the next sync_seq value.
func (db *DB) NextSyncSeq() int64 {
	return db.syncSeq.Add(1)
}

// WithRead executes a read-only function against the database.
// No lock needed — WAL allows concurrent reads.
func (db *DB) WithRead(fn func(conn *sql.DB) error) error {
	return fn(db.conn)
}

// ExecRaw exposes raw SQL execution (used by tests).
func (db *DB) ExecRaw(query string, args ...any) error {
	_, err := db.conn.Exec(query, args...)
	return err
}

// VacuumInto creates a consistent snapshot of the database at dstPath.
func (db *DB) VacuumInto(dstPath string) error {
	_, err := db.conn.Exec(`VACUUM INTO ?`, dstPath)
	return err
}

// QueryRow exposes raw single-row queries (used by tests).
func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.conn.QueryRow(query, args...)
}

// WithWrite executes a write function inside a transaction with mutex.
func (db *DB) WithWrite(fn func(tx *sql.Tx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func reportPhase(cb PhaseFunc, name string, started time.Time) {
	if cb != nil {
		cb(name, time.Since(started))
	}
}
