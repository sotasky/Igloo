package db

import (
	"database/sql"
	"fmt"
	"time"
)

type schemaMigration struct {
	name  string
	apply func(*sql.Tx) error
}

func schemaMigrationStatements() []string {
	return []string{schemaMigrationLedgerStatement()}
}

func schemaMigrationLedgerStatement() string {
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at_ms INTEGER NOT NULL
	) WITHOUT ROWID`
}

var schemaMigrations = []schemaMigration{
	{
		name:  "20260718_add_videos_is_temp",
		apply: addVideosIsTempColumn,
	},
}

// ApplySchemaMigrations advances an existing database through the ordered,
// named transitions required by the current schema. Each migration and its
// ledger entry commit together, so a failed upgrade can be retried safely.
func ApplySchemaMigrations(conn *sql.DB) error {
	if _, err := conn.Exec(schemaMigrationLedgerStatement()); err != nil {
		return fmt.Errorf("create schema migration ledger: %w", err)
	}
	for _, migration := range schemaMigrations {
		if _, err := runSchemaMigrationOnce(conn, migration); err != nil {
			return err
		}
	}
	return nil
}

func runSchemaMigrationOnce(conn *sql.DB, migration schemaMigration) (bool, error) {
	tx, err := conn.Begin()
	if err != nil {
		return false, fmt.Errorf("begin schema migration %s: %w", migration.name, err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = ?)`, migration.name).Scan(&exists); err != nil {
		return false, fmt.Errorf("check schema migration %s: %w", migration.name, err)
	}
	if exists {
		return false, tx.Commit()
	}

	if err := migration.apply(tx); err != nil {
		return false, fmt.Errorf("run schema migration %s: %w", migration.name, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (name, applied_at_ms) VALUES (?, ?)`,
		migration.name,
		time.Now().UnixMilli(),
	); err != nil {
		return false, fmt.Errorf("record schema migration %s: %w", migration.name, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit schema migration %s: %w", migration.name, err)
	}
	return true, nil
}

func addVideosIsTempColumn(tx *sql.Tx) error {
	hasColumn, err := schemaColumnExists(tx, "videos", "is_temp")
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE videos ADD COLUMN is_temp INTEGER DEFAULT 0`); err != nil {
		return err
	}
	return nil
}

type schemaColumnQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func schemaColumnExists(conn schemaColumnQuerier, table, column string) (bool, error) {
	rows, err := conn.Query(`PRAGMA table_xinfo(` + quoteSchemaIdentifier(table) + `)`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}
