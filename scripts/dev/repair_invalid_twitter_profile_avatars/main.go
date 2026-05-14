// repair_invalid_twitter_profile_avatars clears bogus Twitter avatar source URLs
// from channel_profiles so the profile worker can refetch the real profile image.
//
// Usage:
//
//	go run ./scripts/dev/repair_invalid_twitter_profile_avatars -dry-run
//	go run ./scripts/dev/repair_invalid_twitter_profile_avatars
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
	dryRun  = flag.Bool("dry-run", false, "print planned repair without writing")
)

func main() {
	flag.Parse()
	log.SetFlags(0)

	mode := "rw"
	if *dryRun {
		mode = "ro"
	}
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=%s&_journal_mode=WAL&_busy_timeout=10000", dbPath, mode))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	count, err := countInvalid(conn)
	if err != nil {
		log.Fatalf("count invalid avatars: %v", err)
	}
	fmt.Printf("invalid twitter profile avatar rows: %d\n", count)
	if *dryRun {
		fmt.Println("dry-run: no changes applied")
		return
	}
	if count == 0 {
		return
	}

	res, err := conn.Exec(`
		UPDATE channel_profiles
		SET avatar_url = NULL,
		    fetched_at = 0,
		    fail_count = 0,
		    next_retry_at = 0
		WHERE platform = 'twitter'
		  AND tombstone = 0
		  AND COALESCE(avatar_url, '') LIKE 'http%'
		  AND LOWER(avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
	`)
	if err != nil {
		log.Fatalf("repair invalid avatars: %v", err)
	}
	n, _ := res.RowsAffected()
	fmt.Printf("cleared invalid twitter profile avatar rows: %d\n", n)
}

func countInvalid(conn *sql.DB) (int64, error) {
	var count int64
	err := conn.QueryRow(`
		SELECT COUNT(*)
		FROM channel_profiles
		WHERE platform = 'twitter'
		  AND tombstone = 0
		  AND COALESCE(avatar_url, '') LIKE 'http%'
		  AND LOWER(avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
	`).Scan(&count)
	return count, err
}
