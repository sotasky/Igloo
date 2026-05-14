// reset_failed_downloads resets every download_queue row with status='failed'
// back to status='pending' with retry_count=0 so the downloadpool will attempt
// them again. Used to force a retry after the 5-retry permanent-failure cap
// has been hit — the regular scheduler sweep only resets rows with
// retry_count < 5, so anything at the cap would otherwise stay stuck forever.
//
// Usage:
//
//	go run scripts/dev/reset_failed_downloads/main.go           # apply
//	go run scripts/dev/reset_failed_downloads/main.go -dry-run  # preview
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var (
	dataDir = os.ExpandEnv("$HOME/.local/share/igloo")
	dbPath  = filepath.Join(dataDir, "igloo.db")
	dryRun  = flag.Bool("dry-run", false, "print what would be updated without updating")
)

func main() {
	flag.Parse()

	db, err := sql.Open("sqlite", "file:"+dbPath+"?_journal_mode=WAL")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	var failedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM download_queue WHERE status='failed'`).Scan(&failedCount); err != nil {
		log.Fatalf("count failed: %v", err)
	}
	fmt.Printf("failed download_queue rows: %d\n", failedCount)

	rows, err := db.Query(`
		SELECT channel_id, COUNT(*)
		FROM download_queue
		WHERE status='failed'
		GROUP BY channel_id
		ORDER BY COUNT(*) DESC
	`)
	if err != nil {
		log.Fatalf("breakdown: %v", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var ch string
		var n int
		if err := rows.Scan(&ch, &n); err != nil {
			log.Fatalf("scan: %v", err)
		}
		fmt.Printf("  %-70s %d\n", ch, n)
	}

	if *dryRun {
		fmt.Println("dry-run: no changes applied")
		return
	}

	res, err := db.Exec(`
		UPDATE download_queue
		SET status='pending', retry_count=0, started_at=NULL, error=NULL
		WHERE status='failed'
	`)
	if err != nil {
		log.Fatalf("update: %v", err)
	}
	affected, _ := res.RowsAffected()
	fmt.Printf("done: reset %d failed rows to pending (retry_count=0)\n", affected)
}
