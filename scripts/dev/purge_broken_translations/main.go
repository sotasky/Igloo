// purge_broken_translations deletes cached translations whose source text
// contained @mentions, #hashtags, or URLs. Those were stripped entirely by
// the previous stripForTranslate implementation, producing translations with
// dropped content. Re-translation (now using placeholder protection) will
// preserve them.
//
// Usage: go run scripts/dev/purge_broken_translations/main.go [-dry-run]
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
	dryRun  = flag.Bool("dry-run", false, "report how many rows would be purged without deleting")
)

const countSQL = `
SELECT COUNT(*) FROM translations
WHERE (field = 'body' AND tweet_id IN (
    SELECT tweet_id FROM feed_items
    WHERE body_text LIKE '%@%' OR body_text LIKE '%#%' OR body_text LIKE '%http%'
))
OR (field = 'quote' AND tweet_id IN (
    SELECT tweet_id FROM feed_items
    WHERE quote_body_text LIKE '%@%' OR quote_body_text LIKE '%#%' OR quote_body_text LIKE '%http%'
))
`

const deleteSQL = `
DELETE FROM translations
WHERE (field = 'body' AND tweet_id IN (
    SELECT tweet_id FROM feed_items
    WHERE body_text LIKE '%@%' OR body_text LIKE '%#%' OR body_text LIKE '%http%'
))
OR (field = 'quote' AND tweet_id IN (
    SELECT tweet_id FROM feed_items
    WHERE quote_body_text LIKE '%@%' OR quote_body_text LIKE '%#%' OR quote_body_text LIKE '%http%'
))
`

func main() {
	flag.Parse()

	mode := "rw"
	if *dryRun {
		mode = "ro"
	}
	db, err := sql.Open("sqlite", dbPath+"?mode="+mode+"&_journal_mode=WAL")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var totalTranslations int
	if err := db.QueryRow("SELECT COUNT(*) FROM translations").Scan(&totalTranslations); err != nil {
		log.Fatalf("count total: %v", err)
	}

	var affected int
	if err := db.QueryRow(countSQL).Scan(&affected); err != nil {
		log.Fatalf("count affected: %v", err)
	}

	fmt.Printf("translations total: %d\naffected (source had @/#/http): %d\n", totalTranslations, affected)

	if *dryRun {
		fmt.Println("dry-run: no deletions")
		return
	}
	if affected == 0 {
		fmt.Println("nothing to purge")
		return
	}

	res, err := db.Exec(deleteSQL)
	if err != nil {
		log.Fatalf("delete: %v", err)
	}
	deleted, _ := res.RowsAffected()
	fmt.Printf("purged %d translations; they will re-populate on next view\n", deleted)
}
