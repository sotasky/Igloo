package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func getLogsDir() string {
	if d := os.Getenv("IGLOO_DATA_DIR"); d != "" {
		return filepath.Join(d, "logs")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "igloo", "logs")
}

// listLogFiles returns available log files with sizes.
func listLogFiles() (string, error) {
	logsDir := getLogsDir()

	var files []struct {
		path string
		size int64
	}

	filepath.WalkDir(logsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(logsDir, path)
		files = append(files, struct {
			path string
			size int64
		}{rel, info.Size()})
		return nil
	})

	if len(files) == 0 {
		return fmt.Sprintf("No log files found in %s", logsDir), nil
	}

	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })

	var sb strings.Builder
	fmt.Fprintf(&sb, "Log directory: %s\n\n", logsDir)
	fmt.Fprintf(&sb, "%-50s %s\n", "File", "Size")
	sb.WriteString(strings.Repeat("-", 62) + "\n")
	for _, f := range files {
		sizeStr := formatSize(f.size)
		fmt.Fprintf(&sb, "%-50s %s\n", f.path, sizeStr)
	}
	return sb.String(), nil
}

// readLog reads the last N lines of a log file, optionally filtered by a grep pattern.
func readLog(name string, lines int, grepPattern string) (string, error) {
	if lines <= 0 {
		lines = 100
	}
	if lines > 1000 {
		lines = 1000
	}

	logsDir := getLogsDir()
	logPath := filepath.Join(logsDir, name)

	// Security: ensure path stays within logs dir
	abs, err := filepath.Abs(logPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	absLogs, _ := filepath.Abs(logsDir)
	if !strings.HasPrefix(abs, absLogs) {
		return "", fmt.Errorf("path traversal not allowed")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		// Try common log file names
		if !strings.Contains(name, "/") {
			candidates := []string{
				name + ".log",
				name + "/current.log",
				"android/" + name,
			}
			for _, c := range candidates {
				p := filepath.Join(logsDir, c)
				if d, err2 := os.ReadFile(p); err2 == nil {
					data = d
					logPath = p
					err = nil
					break
				}
			}
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
	}

	content := string(data)
	allLines := strings.Split(content, "\n")

	// Remove trailing empty line
	if len(allLines) > 0 && allLines[len(allLines)-1] == "" {
		allLines = allLines[:len(allLines)-1]
	}

	start := 0
	if len(allLines) > lines {
		start = len(allLines) - lines
	}
	tail := allLines[start:]

	// Apply grep filter if specified
	if grepPattern != "" {
		re, err := regexp.Compile("(?i)" + grepPattern)
		if err != nil {
			return "", fmt.Errorf("invalid grep pattern: %w", err)
		}
		var filtered []string
		for i, line := range tail {
			if re.MatchString(line) {
				if i > 0 && (len(filtered) == 0 || filtered[len(filtered)-1] != tail[i-1]) {
					filtered = append(filtered, tail[i-1])
				}
				filtered = append(filtered, line)
				if i+1 < len(tail) {
					filtered = append(filtered, tail[i+1])
				}
			}
		}
		tail = filtered
	}

	var sb strings.Builder
	rel, _ := filepath.Rel(logsDir, logPath)
	fmt.Fprintf(&sb, "=== %s (last %d of %d lines) ===\n\n", rel, len(tail), len(allLines))
	for _, l := range tail {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

// searchLogs greps across all log files for a pattern.
func searchLogs(pattern, file string, contextLines int) (string, error) {
	if contextLines <= 0 {
		contextLines = 2
	}

	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	logsDir := getLogsDir()
	var results []string
	matchCount := 0
	const maxMatches = 100

	searchFile := func(logPath, relName string) {
		if matchCount >= maxMatches {
			return
		}
		data, err := os.ReadFile(logPath)
		if err != nil {
			return
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if matchCount >= maxMatches {
				break
			}
			if re.MatchString(line) {
				matchCount++
				start := i - contextLines
				if start < 0 {
					start = 0
				}
				end := i + contextLines + 1
				if end > len(lines) {
					end = len(lines)
				}
				var block []string
				for j := start; j < end; j++ {
					prefix := "  "
					if j == i {
						prefix = "> "
					}
					block = append(block, fmt.Sprintf("%s%d: %s", prefix, j+1, lines[j]))
				}
				results = append(results, fmt.Sprintf("[%s:%d]\n%s", relName, i+1, strings.Join(block, "\n")))
			}
		}
	}

	if file != "" {
		logPath := filepath.Join(logsDir, file)
		abs, err := filepath.Abs(logPath)
		if err != nil {
			return "", fmt.Errorf("invalid path: %w", err)
		}
		absLogs, _ := filepath.Abs(logsDir)
		if !strings.HasPrefix(abs, absLogs) {
			return "", fmt.Errorf("path traversal not allowed")
		}
		searchFile(logPath, file)
	} else {
		filepath.WalkDir(logsDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(logsDir, path)
			searchFile(path, rel)
			return nil
		})
	}

	if len(results) == 0 {
		return fmt.Sprintf("No matches for '%s'", pattern), nil
	}

	truncated := ""
	if matchCount >= maxMatches {
		truncated = fmt.Sprintf("\n\n(truncated at %d matches)", maxMatches)
	}
	return fmt.Sprintf("%d matches for '%s':\n\n%s%s", matchCount, pattern, strings.Join(results, "\n\n"), truncated), nil
}

// recentErrors scans log files for recent errors.
func recentErrors(minutes int, source string) (string, error) {
	if minutes <= 0 {
		minutes = 60
	}

	logsDir := getLogsDir()
	errorRe := regexp.MustCompile(`(?i)(ERROR|FATAL|panic:|goroutine \d+)`)
	const maxEntries = 50

	type errorEntry struct {
		file    string
		line    int
		content string
		source  string
	}

	var entries []errorEntry

	classifySource := func(relpath string) string {
		if strings.Contains(relpath, "android") {
			return "android"
		}
		if strings.Contains(relpath, "nginx") || strings.Contains(relpath, "error.log") {
			return "nginx"
		}
		return "server"
	}

	filepath.WalkDir(logsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(logsDir, path)
		fileSource := classifySource(rel)
		if source != "" && fileSource != source {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		if time.Since(info.ModTime()).Minutes() > float64(minutes*2) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")

		for i := len(lines) - 1; i >= 0 && len(entries) < maxEntries; i-- {
			if errorRe.MatchString(lines[i]) {
				entries = append(entries, errorEntry{
					file:    rel,
					line:    i + 1,
					content: lines[i],
					source:  fileSource,
				})
			}
		}
		return nil
	})

	if len(entries) == 0 {
		return fmt.Sprintf("No errors found in the last %d minutes", minutes), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d errors found (last %d minutes):\n\n", len(entries), minutes)
	for _, e := range entries {
		content := e.content
		if len(content) > 150 {
			content = content[:147] + "..."
		}
		fmt.Fprintf(&sb, "[%s] %s:%d\n  %s\n\n", e.source, e.file, e.line, content)
	}

	if len(entries) >= maxEntries {
		sb.WriteString(fmt.Sprintf("(truncated at %d entries)\n", maxEntries))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
