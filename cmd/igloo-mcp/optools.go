package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	queues := []struct {
		name, table, statusCol, errorCol, timeCol string
	}{
		{"Download Queue", "download_queue", "status", "error", "added_at"},
		{"Feed Media Jobs", "feed_media_jobs", "status", "last_error", "updated_at"},
		{"Channel Queue", "channel_queue", "status", "", "added_at"},
	}

	for _, q := range queues {
		fmt.Fprintf(&sb, "%s (%s):\n", q.name, q.table)

		// Counts by status
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

		// Oldest pending
		var oldest string
		err = conn.QueryRow(fmt.Sprintf("SELECT %s FROM %s WHERE %s IN ('pending', 'queued') ORDER BY %s ASC LIMIT 1",
			q.timeCol, q.table, q.statusCol, q.timeCol)).Scan(&oldest)
		if err == nil {
			fmt.Fprintf(&sb, "  oldest pending: %s\n", oldest)
		}

		// Stuck jobs (processing > 10 min)
		var stuckCount int
		err = conn.QueryRow(fmt.Sprintf(
			"SELECT COUNT(*) FROM %s WHERE %s IN ('processing', 'running') AND %s < datetime('now', '-10 minutes')",
			q.table, q.statusCol, q.timeCol)).Scan(&stuckCount)
		if err == nil && stuckCount > 0 {
			fmt.Fprintf(&sb, "  STUCK: %d jobs processing > 10 min\n", stuckCount)
		}

		// Last 5 errors
		if q.errorCol == "" {
			sb.WriteString("\n")
			continue
		}
		errRows, err := conn.Query(fmt.Sprintf(
			"SELECT %s, %s FROM %s WHERE %s IN ('error', 'failed') ORDER BY %s DESC LIMIT 5",
			q.timeCol, q.errorCol, q.table, q.statusCol, q.timeCol))
		if err == nil {
			var errors []string
			for errRows.Next() {
				var ts, errMsg string
				_ = errRows.Scan(&ts, &errMsg)
				if len(errMsg) > 100 {
					errMsg = errMsg[:97] + "..."
				}
				errors = append(errors, fmt.Sprintf("    %s: %s", ts, errMsg))
			}
			_ = errRows.Close()
			if len(errors) > 0 {
				sb.WriteString("  recent errors:\n")
				for _, e := range errors {
					sb.WriteString(e + "\n")
				}
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}
