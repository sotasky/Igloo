// repair_invalid_feed_avatar_urls clears bogus feed avatar URLs such as
// https://x.com/<handle>/status/undefined from feed_items.
//
// Usage:
//
//	go run ./scripts/dev/repair_invalid_feed_avatar_urls -dry-run
//	go run ./scripts/dev/repair_invalid_feed_avatar_urls
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/screwys/igloo/internal/model"
	_ "modernc.org/sqlite"
)

var (
	dataDir = os.ExpandEnv("$HOME/.local/share/igloo")
	dbPath  = filepath.Join(dataDir, "igloo.db")
	dryRun  = flag.Bool("dry-run", false, "print planned repair without writing")
)

type badRow struct {
	tweetID     string
	authorURL   string
	quoteURL    string
	clearAuthor bool
	clearQuote  bool
}

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

	rows, err := findBadRows(conn)
	if err != nil {
		log.Fatalf("find bad feed avatar urls: %v", err)
	}
	var authorFields, quoteFields int
	for _, row := range rows {
		if row.clearAuthor {
			authorFields++
		}
		if row.clearQuote {
			quoteFields++
		}
	}
	fmt.Printf("feed rows with invalid avatar URLs: %d\n", len(rows))
	fmt.Printf("invalid author_avatar_url fields: %d\n", authorFields)
	fmt.Printf("invalid quote_author_avatar_url fields: %d\n", quoteFields)
	if *dryRun {
		fmt.Println("dry-run: no changes applied")
		return
	}
	if len(rows) == 0 {
		return
	}

	tx, err := conn.Begin()
	if err != nil {
		log.Fatalf("begin: %v", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var seq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sync_seq), 0) FROM feed_items`).Scan(&seq); err != nil {
		log.Fatalf("max sync_seq: %v", err)
	}
	stmt, err := tx.Prepare(`
		UPDATE feed_items
		SET author_avatar_url = CASE WHEN ? != 0 THEN NULL ELSE author_avatar_url END,
		    quote_author_avatar_url = CASE WHEN ? != 0 THEN NULL ELSE quote_author_avatar_url END,
		    sync_seq = ?
		WHERE tweet_id = ?
	`)
	if err != nil {
		log.Fatalf("prepare update: %v", err)
	}
	defer func() {
		_ = stmt.Close()
	}()

	for _, row := range rows {
		seq++
		clearAuthor := 0
		if row.clearAuthor {
			clearAuthor = 1
		}
		clearQuote := 0
		if row.clearQuote {
			clearQuote = 1
		}
		if _, err := stmt.Exec(clearAuthor, clearQuote, seq, row.tweetID); err != nil {
			log.Fatalf("repair %s: %v", row.tweetID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}
	fmt.Printf("cleared invalid feed avatar URL fields: %d\n", authorFields+quoteFields)
	fmt.Printf("bumped sync_seq through: %d\n", seq)
}

func findBadRows(conn *sql.DB) ([]badRow, error) {
	rows, err := conn.Query(`
		SELECT tweet_id,
		       COALESCE(author_avatar_url, ''),
		       COALESCE(quote_author_avatar_url, '')
		FROM feed_items
		WHERE LOWER(COALESCE(author_avatar_url, '')) LIKE '%/status/%'
		   OR LOWER(COALESCE(quote_author_avatar_url, '')) LIKE '%/status/%'
		ORDER BY tweet_id
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []badRow
	for rows.Next() {
		var row badRow
		if err := rows.Scan(&row.tweetID, &row.authorURL, &row.quoteURL); err != nil {
			return nil, err
		}
		row.clearAuthor = model.IsInvalidTwitterAvatarURL(row.authorURL)
		row.clearQuote = model.IsInvalidTwitterAvatarURL(row.quoteURL)
		if row.clearAuthor || row.clearQuote {
			out = append(out, row)
		}
	}
	return out, rows.Err()
}
