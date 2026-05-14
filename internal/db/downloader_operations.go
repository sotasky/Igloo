package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/screwys/igloo/internal/model"
)

const (
	DownloaderOperationMaxRows = 5000
	DownloaderOperationMaxAge  = 7 * 24 * time.Hour
)

func (db *DB) RecordDownloaderOperation(ctx context.Context, op model.DownloaderOperation) error {
	if op.Operation == "" || op.Platform == "" || op.Tool == "" {
		return nil
	}
	if op.StartedAtMs == 0 {
		op.StartedAtMs = time.Now().UnixMilli()
	}
	if op.EndedAtMs == 0 {
		op.EndedAtMs = time.Now().UnixMilli()
	}
	if op.ElapsedMs == 0 && op.EndedAtMs >= op.StartedAtMs {
		op.ElapsedMs = op.EndedAtMs - op.StartedAtMs
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO downloader_operations (
				operation, platform, subject, tool, started_at_ms, ended_at_ms,
				status, error_kind, error, cookie_label, elapsed_ms,
				item_count, media_count, file_count, bytes, summary_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			op.Operation, op.Platform, op.Subject, op.Tool, op.StartedAtMs, op.EndedAtMs,
			op.Status, op.ErrorKind, op.Error, op.CookieLabel, op.ElapsedMs,
			op.ItemCount, op.MediaCount, op.FileCount, op.Bytes, op.SummaryJSON,
		)
		return err
	})
}

func (db *DB) ListDownloaderOperations(limit int) ([]model.DownloaderOperation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.conn.Query(`
		SELECT id, operation, platform, subject, tool, started_at_ms, ended_at_ms,
			status, error_kind, error, cookie_label, elapsed_ms, item_count,
			media_count, file_count, bytes, summary_json
		FROM downloader_operations
		ORDER BY started_at_ms DESC, id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []model.DownloaderOperation
	for rows.Next() {
		var op model.DownloaderOperation
		if err := rows.Scan(
			&op.ID, &op.Operation, &op.Platform, &op.Subject, &op.Tool, &op.StartedAtMs, &op.EndedAtMs,
			&op.Status, &op.ErrorKind, &op.Error, &op.CookieLabel, &op.ElapsedMs, &op.ItemCount,
			&op.MediaCount, &op.FileCount, &op.Bytes, &op.SummaryJSON,
		); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func (db *DB) DownloaderOperationCountsSince(startedAtMs int64) ([]map[string]any, error) {
	rows, err := db.conn.Query(`
		SELECT platform, operation, status, error_kind, COUNT(*)
		FROM downloader_operations
		WHERE started_at_ms >= ?
		GROUP BY platform, operation, status, error_kind
		ORDER BY platform, operation, status, error_kind`, startedAtMs)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var out []map[string]any
	for rows.Next() {
		var platform, operation, status, errorKind string
		var count int
		if err := rows.Scan(&platform, &operation, &status, &errorKind, &count); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"platform":   platform,
			"operation":  operation,
			"status":     status,
			"error_kind": errorKind,
			"count":      count,
		})
	}
	return out, rows.Err()
}

func (db *DB) PruneDownloaderOperations(maxRows int, maxAge time.Duration) error {
	if maxRows <= 0 {
		maxRows = DownloaderOperationMaxRows
	}
	if maxAge <= 0 {
		maxAge = DownloaderOperationMaxAge
	}
	cutoff := time.Now().Add(-maxAge).UnixMilli()
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM downloader_operations WHERE started_at_ms < ?`, cutoff); err != nil {
			return err
		}
		_, err := tx.Exec(`
			DELETE FROM downloader_operations
			WHERE id NOT IN (
				SELECT id FROM downloader_operations
				ORDER BY started_at_ms DESC, id DESC
				LIMIT ?
			)`, maxRows)
		return err
	})
}
