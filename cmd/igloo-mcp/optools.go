package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// systemStatus checks the health of all Igloo services.
func systemStatus() (string, error) {
	var sb strings.Builder
	sb.WriteString("=== Igloo System Status ===\n\n")

	// igloo.service
	sb.WriteString("igloo.service:\n")
	out, err := exec.Command("systemctl", "--user", "status", "igloo.service", "--no-pager", "-l").CombinedOutput()
	if err != nil {
		sb.WriteString("  " + strings.TrimSpace(string(out)) + "\n")
	} else {
		for _, line := range strings.Split(string(out), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Active:") || strings.HasPrefix(trimmed, "Memory:") ||
				strings.HasPrefix(trimmed, "CPU:") || strings.HasPrefix(trimmed, "Main PID:") {
				sb.WriteString("  " + trimmed + "\n")
			}
		}
	}
	sb.WriteString("\n")

	// nginx
	sb.WriteString("nginx:\n")
	out, err = exec.Command("pgrep", "-x", "nginx").CombinedOutput()
	if err != nil {
		sb.WriteString("  not running\n")
	} else {
		pids := strings.TrimSpace(string(out))
		sb.WriteString("  running (PIDs: " + strings.ReplaceAll(pids, "\n", ", ") + ")\n")
	}
	sb.WriteString("\n")

	// Port binding
	port := os.Getenv("IGLOO_PORT")
	if port == "" {
		port = "5001"
	}
	sb.WriteString("port " + port + ":\n")
	out, _ = exec.Command("ss", "-tlnp", "sport", "=", ":"+port).CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if strings.Contains(outStr, ":"+port) {
		sb.WriteString("  bound\n")
	} else {
		sb.WriteString("  not bound\n")
	}
	sb.WriteString("\n")

	// Disk space
	dataDir := getDBPath()
	dir := filepath.Dir(dataDir)
	sb.WriteString("disk (" + dir + "):\n")
	out, err = exec.Command("df", "-h", dir).CombinedOutput()
	if err != nil {
		sb.WriteString("  error: " + strings.TrimSpace(string(out)) + "\n")
	} else {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 2 {
			sb.WriteString("  " + lines[len(lines)-1] + "\n")
		}
	}
	sb.WriteString("\n")

	// DB file size
	sb.WriteString("database:\n")
	if info, err := os.Stat(dataDir); err == nil {
		sb.WriteString("  " + dataDir + ": " + formatDBSize(info.Size()) + "\n")
	} else {
		sb.WriteString("  " + dataDir + ": not found\n")
	}

	return sb.String(), nil
}

func formatDBSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

// pipelineStatus returns queue health for all processing pipelines.
func pipelineStatus() (string, error) {
	conn, err := getServerDB()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("=== Pipeline Status ===\n\n")

	queues := []pipelineQueue{
		{
			name:           "Download Queue",
			table:          "download_queue",
			statusCol:      "status",
			timeCol:        "added_at_ms",
			startCol:       "started_at_ms",
			nextAttemptCol: "next_attempt_at_ms",
			leaseUntilCol:  "lease_until_ms",
			errorCol:       "last_error",
			errorKindCol:   "last_error_kind",
			pendingStates:  []string{"pending"},
			activeStates:   []string{"processing"},
			failedStates:   []string{"blocked"},
		},
		{
			name:           "Translation Jobs",
			table:          "translation_jobs",
			statusCol:      "status",
			timeCol:        "updated_at",
			nextAttemptCol: "next_attempt_at",
			errorCol:       "last_error",
			errorKindCol:   "last_error_kind",
			pendingStates:  []string{"pending", "queued"},
			activeStates:   []string{"running"},
			failedStates:   []string{"failed", "error"},
		},
		{
			name:           "Media Objects",
			table:          "media_objects",
			statusCol:      "job_state",
			timeCol:        "updated_at_ms",
			nextAttemptCol: "next_attempt_at_ms",
			leaseUntilCol:  "lease_until_ms",
			errorCol:       "last_error",
			errorKindCol:   "last_error_kind",
			pendingStates:  []string{"queued"},
			activeStates:   []string{"downloading"},
			failedStates:   []string{"failed", "server_missing"},
		},
	}

	nowMs := time.Now().UnixMilli()
	stuckCutoffMs := nowMs - int64(10*time.Minute/time.Millisecond)
	for _, q := range queues {
		fmt.Fprintf(&sb, "%s (%s):\n", q.name, q.table)

		rows, err := conn.Query(fmt.Sprintf("SELECT %s, COUNT(*) FROM %s GROUP BY %s ORDER BY COUNT(*) DESC", q.statusCol, q.table, q.statusCol))
		if err != nil {
			fmt.Fprintf(&sb, "  error: %v\n\n", err)
			continue
		}
		var parts []string
		for rows.Next() {
			var status string
			var count int
			_ = rows.Scan(&status, &count)
			parts = append(parts, fmt.Sprintf("%s=%d", status, count))
		}
		_ = rows.Close()
		if len(parts) > 0 {
			fmt.Fprintf(&sb, "  counts: %s\n", strings.Join(parts, ", "))
		} else {
			sb.WriteString("  counts: (empty)\n")
		}

		if ready, err := pipelineReadyCount(conn, q, nowMs); err == nil {
			fmt.Fprintf(&sb, "  ready to claim: %d\n", ready)
		}
		if oldest, err := pipelineOldestPending(conn, q); err == nil && oldest > 0 {
			fmt.Fprintf(&sb, "  oldest pending: %s\n", formatMillis(oldest))
		}
		if activeLeases, expiredLeases, ok := pipelineLeaseCounts(conn, q, nowMs); ok {
			fmt.Fprintf(&sb, "  leases: active=%d expired=%d\n", activeLeases, expiredLeases)
		}
		if stuckCount, err := pipelineStuckCount(conn, q, stuckCutoffMs); err == nil && stuckCount > 0 {
			fmt.Fprintf(&sb, "  stuck active >10m: %d\n", stuckCount)
		}

		if kinds, err := pipelineErrorKinds(conn, q); err == nil && len(kinds) > 0 {
			fmt.Fprintf(&sb, "  error kinds: %s\n", strings.Join(kinds, ", "))
		}
		if errors, err := pipelineRecentErrors(conn, q); err == nil && len(errors) > 0 {
			sb.WriteString("  recent errors:\n")
			for _, e := range errors {
				sb.WriteString(e + "\n")
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

type pipelineQueue struct {
	name           string
	table          string
	statusCol      string
	timeCol        string
	startCol       string
	nextAttemptCol string
	leaseUntilCol  string
	errorCol       string
	errorKindCol   string
	pendingStates  []string
	activeStates   []string
	failedStates   []string
}

func pipelineReadyCount(conn *sql.DB, q pipelineQueue, nowMs int64) (int, error) {
	if len(q.pendingStates) == 0 {
		return 0, nil
	}
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s IN (%s)", q.table, q.statusCol, sqlStringList(q.pendingStates))
	args := []any{}
	if q.nextAttemptCol != "" {
		query += fmt.Sprintf(" AND (%s = 0 OR %s <= ?)", q.nextAttemptCol, q.nextAttemptCol)
		args = append(args, nowMs)
	}
	var count int
	err := conn.QueryRow(query, args...).Scan(&count)
	return count, err
}

func pipelineOldestPending(conn *sql.DB, q pipelineQueue) (int64, error) {
	if len(q.pendingStates) == 0 || q.timeCol == "" {
		return 0, sql.ErrNoRows
	}
	var oldest int64
	err := conn.QueryRow(fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s IN (%s) AND %s > 0 ORDER BY %s ASC LIMIT 1",
		q.timeCol,
		q.table,
		q.statusCol,
		sqlStringList(q.pendingStates),
		q.timeCol,
		q.timeCol,
	)).Scan(&oldest)
	return oldest, err
}

func pipelineLeaseCounts(conn *sql.DB, q pipelineQueue, nowMs int64) (int, int, bool) {
	if q.leaseUntilCol == "" || len(q.activeStates) == 0 {
		return 0, 0, false
	}
	var active, expired int
	_ = conn.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE %s IN (%s) AND %s > ?",
		q.table,
		q.statusCol,
		sqlStringList(q.activeStates),
		q.leaseUntilCol,
	), nowMs).Scan(&active)
	_ = conn.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE %s IN (%s) AND (%s = 0 OR %s <= ?)",
		q.table,
		q.statusCol,
		sqlStringList(q.activeStates),
		q.leaseUntilCol,
		q.leaseUntilCol,
	), nowMs).Scan(&expired)
	return active, expired, true
}

func pipelineStuckCount(conn *sql.DB, q pipelineQueue, cutoffMs int64) (int, error) {
	if len(q.activeStates) == 0 {
		return 0, nil
	}
	timeCol := q.startCol
	if timeCol == "" {
		timeCol = q.timeCol
	}
	if timeCol == "" {
		return 0, nil
	}
	var count int
	err := conn.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE %s IN (%s) AND %s > 0 AND %s < ?",
		q.table,
		q.statusCol,
		sqlStringList(q.activeStates),
		timeCol,
		timeCol,
	), cutoffMs).Scan(&count)
	return count, err
}

func pipelineErrorKinds(conn *sql.DB, q pipelineQueue) ([]string, error) {
	if q.errorKindCol == "" || q.errorKindCol == q.statusCol {
		return nil, nil
	}
	where := fmt.Sprintf("%s != ''", q.errorKindCol)
	if len(q.failedStates) > 0 {
		where = fmt.Sprintf("%s IN (%s)", q.statusCol, sqlStringList(q.failedStates))
	}
	rows, err := conn.Query(fmt.Sprintf(
		"SELECT COALESCE(NULLIF(%s, ''), 'unclassified'), COUNT(*) FROM %s WHERE %s GROUP BY 1 ORDER BY COUNT(*) DESC, 1 LIMIT 8",
		q.errorKindCol, q.table, where,
	))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var parts []string
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%d", maskSensitive(kind), count))
	}
	return parts, nil
}

func pipelineRecentErrors(conn *sql.DB, q pipelineQueue) ([]string, error) {
	if q.errorCol == "" {
		return nil, nil
	}
	where := fmt.Sprintf("%s != ''", q.errorCol)
	if len(q.failedStates) > 0 {
		where = fmt.Sprintf("%s IN (%s)", q.statusCol, sqlStringList(q.failedStates))
	}
	rows, err := conn.Query(fmt.Sprintf(
		"SELECT %s, COALESCE(%s, ''), COALESCE(%s, '') FROM %s WHERE %s ORDER BY %s DESC LIMIT 5",
		q.timeCol,
		q.errorKindSelect(),
		q.errorCol,
		q.table,
		where,
		q.timeCol,
	))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var errors []string
	for rows.Next() {
		var ts int64
		var kind, message string
		if err := rows.Scan(&ts, &kind, &message); err != nil {
			continue
		}
		prefix := formatMillis(ts)
		if kind != "" {
			prefix += " " + maskSensitive(kind)
		}
		errors = append(errors, fmt.Sprintf("    %s: %s", prefix, maskSensitive(compactLong(message, 140))))
	}
	return errors, nil
}

func (q pipelineQueue) errorKindSelect() string {
	if q.errorKindCol == "" {
		return "''"
	}
	return q.errorKindCol
}

func sqlStringList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "'"+strings.ReplaceAll(value, "'", "''")+"'")
	}
	if len(quoted) == 0 {
		return "''"
	}
	return strings.Join(quoted, ", ")
}
