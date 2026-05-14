// purge_broken_media removes media_files entries whose on-disk file is
// missing, empty, or suspiciously small (<100 bytes, i.e. error placeholders).
// Also deletes the broken files from disk.
//
// Usage: go run scripts/dev/purge_broken_media/main.go [-dry-run]
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
	dryRun  = flag.Bool("dry-run", false, "report broken entries without deleting")
)

func main() {
	flag.Parse()

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = db.Close()
	}()

	rows, err := db.Query("SELECT id, owner_type, owner_id, media_index, file_path FROM media_files WHERE file_path != ''")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = rows.Close()
	}()

	type broken struct {
		id      int64
		path    string
		reason  string
		ownerID string
	}
	var entries []broken

	for rows.Next() {
		var id int64
		var ownerType, ownerID, filePath string
		var mediaIndex int
		if err := rows.Scan(&id, &ownerType, &ownerID, &mediaIndex, &filePath); err != nil {
			log.Fatal(err)
		}
		absPath := filepath.Join(dataDir, filePath)
		fi, err := os.Stat(absPath)
		if err != nil {
			entries = append(entries, broken{id, absPath, "missing", ownerID})
		} else if fi.Size() == 0 {
			entries = append(entries, broken{id, absPath, "empty", ownerID})
		} else if fi.Size() < 100 {
			entries = append(entries, broken{id, absPath, fmt.Sprintf("%d bytes", fi.Size()), ownerID})
		}
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("found %d broken media_files entries\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  id=%d owner=%s reason=%s path=%s\n", e.id, e.ownerID, e.reason, e.path)
	}

	if *dryRun || len(entries) == 0 {
		return
	}

	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}
	deleted := 0
	for _, e := range entries {
		if _, err := tx.Exec("DELETE FROM media_files WHERE id = ?", e.id); err != nil {
			log.Printf("delete id=%d: %v", e.id, err)
			continue
		}
		_ = os.Remove(e.path)
		deleted++
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("purged %d broken entries\n", deleted)
}
