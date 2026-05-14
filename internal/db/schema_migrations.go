package db

import (
	"database/sql"
	"fmt"
	"time"
)

func runSchemaMigrationOnce(conn *sql.DB, name string, fn func(*sql.Tx) error) (bool, error) {
	tx, err := conn.Begin()
	if err != nil {
		return false, fmt.Errorf("begin schema migration %s: %w", name, err)
	}

	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&exists); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("check schema migration %s: %w", name, err)
	}
	if exists > 0 {
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit skipped schema migration %s: %w", name, err)
		}
		return false, nil
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("run schema migration %s: %w", name, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (name, applied_at_ms) VALUES (?, ?)`,
		name,
		time.Now().UnixMilli(),
	); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("record schema migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit schema migration %s: %w", name, err)
	}
	return true, nil
}

func schemaMigrationApplied(conn *sql.DB, name string) (bool, error) {
	var exists int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&exists); err != nil {
		return false, fmt.Errorf("check schema migration %s: %w", name, err)
	}
	return exists > 0, nil
}

func recordSchemaMigration(conn *sql.DB, name string) error {
	if _, err := conn.Exec(
		`INSERT OR IGNORE INTO schema_migrations (name, applied_at_ms) VALUES (?, ?)`,
		name,
		time.Now().UnixMilli(),
	); err != nil {
		return fmt.Errorf("record schema migration %s: %w", name, err)
	}
	return nil
}

func (db *DB) runStartupMigrationOnce(name string, fn func() error, warnIfReappeared func() error) error {
	applied, err := schemaMigrationApplied(db.conn, name)
	if err != nil {
		return err
	}
	if applied {
		if warnIfReappeared != nil {
			return warnIfReappeared()
		}
		return nil
	}
	if err := fn(); err != nil {
		return err
	}
	return recordSchemaMigration(db.conn, name)
}
