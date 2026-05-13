package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const defaultQueueLease = 5 * time.Minute

var ErrQueueLeaseNotHeld = errors.New("queue lease not held")

// LeaseOptions describes an atomic queue claim from one status into another.
type LeaseOptions struct {
	Owner      string
	NowMs      int64
	LeaseMs    int64
	Limit      int
	StatusFrom string
	StatusTo   string
}

func normalizeLeaseOptions(opts LeaseOptions, defaultFrom, defaultTo string) LeaseOptions {
	if opts.Owner == "" {
		opts.Owner = "unknown"
	}
	if opts.NowMs == 0 {
		opts.NowMs = time.Now().UnixMilli()
	}
	if opts.LeaseMs <= 0 {
		opts.LeaseMs = defaultQueueLease.Milliseconds()
	}
	if opts.Limit <= 0 {
		opts.Limit = 1
	}
	if opts.StatusFrom == "" {
		opts.StatusFrom = defaultFrom
	}
	if opts.StatusTo == "" {
		opts.StatusTo = defaultTo
	}
	return opts
}

func leaseEligibleSQL() string {
	return leaseEligibleSQLFor("status", "next_attempt_at_ms", "lease_until_ms")
}

func leaseEligibleSQLFor(stateColumn, nextAttemptColumn, leaseUntilColumn string) string {
	return `
		` + nextAttemptColumn + ` <= ?
		AND (
			(` + stateColumn + ` = ? AND (COALESCE(` + leaseUntilColumn + `, 0) = 0 OR ` + leaseUntilColumn + ` <= ?))
			OR (` + stateColumn + ` = ? AND COALESCE(` + leaseUntilColumn + `, 0) <= ?)
		)`
}

func claimLeasedIDs(tx *sql.Tx, table, keyColumn string, candidateQuery string, candidateArgs []any, opts LeaseOptions) ([]string, error) {
	return claimLeasedIDsWithStateColumn(tx, table, keyColumn, "status", candidateQuery, candidateArgs, opts)
}

func claimLeasedIDsWithStateColumn(tx *sql.Tx, table, keyColumn, stateColumn string, candidateQuery string, candidateArgs []any, opts LeaseOptions) ([]string, error) {
	rows, err := tx.Query(candidateQuery, candidateArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		candidates = append(candidates, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	update := fmt.Sprintf(`
		UPDATE %s
		   SET %s = ?,
		       lease_owner = ?,
		       lease_until_ms = ?
		 WHERE %s = ?
		   AND %s
	`, table, stateColumn, keyColumn, leaseEligibleSQLFor(stateColumn, "next_attempt_at_ms", "lease_until_ms"))
	claimed := candidates[:0]
	for _, key := range candidates {
		res, err := tx.Exec(
			update,
			opts.StatusTo, opts.Owner, opts.NowMs+opts.LeaseMs,
			key,
			opts.NowMs, opts.StatusFrom, opts.NowMs, opts.StatusTo, opts.NowMs,
		)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			claimed = append(claimed, key)
		}
	}
	return claimed, nil
}

func requireQueueLeaseUpdate(res sql.Result, table, key, owner string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s %s owner %q", ErrQueueLeaseNotHeld, table, key, owner)
	}
	return nil
}

func jobRetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := 30 * time.Second
	for i := 0; i < attempt && delay < 6*time.Hour; i++ {
		delay *= 2
	}
	if delay > 6*time.Hour {
		return 6 * time.Hour
	}
	return delay
}
