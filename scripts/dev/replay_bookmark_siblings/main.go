// replay_bookmark_siblings bumps sync_seq for current bookmarks and their
// content-hash siblings so Android receives authoritative bookmark state again.
//
// Usage:
//
//	go run ./scripts/dev/replay_bookmark_siblings -dry-run
//	go run ./scripts/dev/replay_bookmark_siblings
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
	dryRun  = flag.Bool("dry-run", false, "print planned replay without updating the database")
)

func main() {
	flag.Parse()

	mode := "rw"
	if *dryRun {
		mode = "ro"
	}
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=%s&_journal_mode=WAL&_busy_timeout=30000", dbPath, mode))
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	rows, err := targetRows(conn)
	if err != nil {
		log.Fatalf("load target rows: %v", err)
	}
	fmt.Printf("target feed_items: %d\n", len(rows))
	for i, row := range rows {
		if i >= 20 {
			fmt.Println("...")
			break
		}
		marker := "sibling"
		if row.directBookmark {
			marker = "bookmark"
		}
		fmt.Printf("  %s %s hash=%s old_seq=%d\n", marker, row.tweetID, row.contentHash, row.syncSeq)
	}
	if *dryRun {
		fmt.Println("dry-run: no changes applied")
		return
	}
	if len(rows) == 0 {
		fmt.Println("nothing to replay")
		return
	}

	maxSeq, err := maxSyncSeq(conn)
	if err != nil {
		log.Fatalf("load max sync_seq: %v", err)
	}
	tx, err := conn.Begin()
	if err != nil {
		log.Fatalf("begin: %v", err)
	}
	for _, row := range rows {
		maxSeq++
		if _, err := tx.Exec(`UPDATE feed_items SET sync_seq = ? WHERE tweet_id = ?`, maxSeq, row.tweetID); err != nil {
			_ = tx.Rollback()
			log.Fatalf("update %s: %v", row.tweetID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}
	fmt.Printf("done: replayed %d feed_items, sync_seq %d..%d\n", len(rows), maxSeq-int64(len(rows))+1, maxSeq)
}

type targetRow struct {
	tweetID        string
	contentHash    string
	syncSeq        int64
	directBookmark bool
}

func targetRows(conn *sql.DB) ([]targetRow, error) {
	rows, err := conn.Query(`
		WITH bookmark_hashes AS (
			SELECT DISTINCT NULLIF(TRIM(COALESCE(fi.content_hash, '')), '') AS content_hash
			  FROM feed_items fi
			  JOIN bookmarks b ON b.video_id = fi.tweet_id
			 WHERE NULLIF(TRIM(COALESCE(fi.content_hash, '')), '') IS NOT NULL
		),
		targets AS (
			SELECT fi.tweet_id
			  FROM feed_items fi
			  JOIN bookmarks b ON b.video_id = fi.tweet_id
			UNION
			SELECT fi.tweet_id
			  FROM feed_items fi
			 WHERE NULLIF(TRIM(COALESCE(fi.content_hash, '')), '') IN (SELECT content_hash FROM bookmark_hashes)
		)
		SELECT fi.tweet_id,
		       COALESCE(fi.content_hash, ''),
		       COALESCE(fi.sync_seq, 0),
		       CASE WHEN b.video_id IS NULL THEN 0 ELSE 1 END
		  FROM feed_items fi
		  JOIN targets t ON t.tweet_id = fi.tweet_id
		  LEFT JOIN bookmarks b ON b.video_id = fi.tweet_id
		 ORDER BY COALESCE(fi.content_hash, ''), fi.tweet_id
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []targetRow
	for rows.Next() {
		var row targetRow
		var directBookmark int
		if err := rows.Scan(&row.tweetID, &row.contentHash, &row.syncSeq, &directBookmark); err != nil {
			return nil, err
		}
		row.directBookmark = directBookmark == 1
		out = append(out, row)
	}
	return out, rows.Err()
}

func maxSyncSeq(conn *sql.DB) (int64, error) {
	var maxSeq int64
	err := conn.QueryRow(`
		SELECT MAX(seq) FROM (
			SELECT COALESCE(MAX(sync_seq), 0) AS seq FROM feed_items
			UNION ALL SELECT COALESCE(MAX(sync_seq), 0) FROM videos
			UNION ALL SELECT COALESCE(MAX(sync_seq), 0) FROM channels
		)
	`).Scan(&maxSeq)
	return maxSeq, err
}
