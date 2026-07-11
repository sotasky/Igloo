package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/language"
	"github.com/screwys/igloo/internal/xfeed"
)

var tokenRe = regexp.MustCompile(`https?://\S+|@\S+|#\S+`)

type exportRow struct {
	RowID      string   `json:"row_id"`
	Field      string   `json:"field"`
	SourceLang string   `json:"source_lang"`
	TargetLang string   `json:"target_lang"`
	Text       string   `json:"text"`
	Context    string   `json:"context,omitempty"`
	Tokens     []string `json:"tokens,omitempty"`
}

type mapRow struct {
	RowID      string   `json:"row_id"`
	TweetID    string   `json:"tweet_id"`
	Field      string   `json:"field"`
	SourceLang string   `json:"source_lang"`
	TargetLang string   `json:"target_lang"`
	Tokens     []string `json:"tokens,omitempty"`
}

type importRow struct {
	RowID          string `json:"row_id"`
	TranslatedText string `json:"translated_text"`
}

type candidateRow struct {
	TweetID       string
	Field         string
	SourceText    string
	SourceLang    string
	BodyText      string
	QuoteBodyText string
}

func main() {
	var mode, dbPath, dataDir, outPath, mapPath, inPath, target, skipRaw string
	var limit int
	var apply bool
	flag.StringVar(&mode, "mode", "dry-run", "dry-run, backfill-lang, export, or import")
	flag.StringVar(&dbPath, "db", defaultDBPath(), "Igloo SQLite DB path")
	flag.StringVar(&dataDir, "data-dir", defaultDataDir(), "Igloo data dir")
	flag.StringVar(&outPath, "out", "translation_batch.jsonl", "export JSONL path")
	flag.StringVar(&mapPath, "map", "translation_batch.map.jsonl", "local row map JSONL path")
	flag.StringVar(&inPath, "in", "translation_results.jsonl", "translated JSONL path for import")
	flag.StringVar(&target, "target", "en", "target language")
	flag.StringVar(&skipRaw, "skip", "", "comma-separated source languages to skip")
	flag.IntVar(&limit, "limit", 100, "maximum rows")
	flag.BoolVar(&apply, "apply", false, "apply mutating modes")
	flag.Parse()

	if limit < 1 {
		limit = 100
	}
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		target = "en"
	}
	skip := splitCSV(skipRaw)

	var database *db.DB
	var err error
	if (mode == "backfill-lang" && apply) || mode == "import" {
		database, err = db.OpenPath(dbPath, dataDir)
	} else {
		database, err = db.OpenReadOnly(dbPath, dataDir)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()

	switch mode {
	case "dry-run":
		if err := runDryRun(context.Background(), database, target, skip, limit); err != nil {
			log.Fatal(err)
		}
	case "backfill-lang":
		if err := runBackfillLang(context.Background(), database, target, skip, limit, apply); err != nil {
			log.Fatal(err)
		}
	case "export":
		if err := runExport(context.Background(), database, target, skip, limit, outPath, mapPath); err != nil {
			log.Fatal(err)
		}
	case "import":
		if !apply {
			log.Fatal("import requires -apply")
		}
		if err := runImport(database, inPath, mapPath); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unknown mode %q", mode)
	}
}

func runDryRun(ctx context.Context, database *db.DB, target string, skip []string, limit int) error {
	rows, err := loadCatchupCandidates(ctx, database, target, skip, limit)
	if err != nil {
		return err
	}
	byField := map[string]int{}
	byLang := map[string]int{}
	for _, row := range rows {
		byField[row.Field]++
		lang := row.SourceLang
		if lang == "" {
			lang = "(empty)"
		}
		byLang[lang]++
	}
	fmt.Printf("eligible rows: %d\n", len(rows))
	printCounts("by field", byField)
	printCounts("by lang", byLang)
	return nil
}

func runBackfillLang(ctx context.Context, database *db.DB, target string, skip []string, limit int, apply bool) error {
	rows, err := loadCatchupCandidates(ctx, database, target, skip, limit)
	if err != nil {
		return err
	}
	type update struct {
		tweetID string
		field   string
		lang    string
	}
	var updates []update
	for _, row := range rows {
		if !language.IsUnknown(row.SourceLang) {
			continue
		}
		detected := xfeed.DetectLang(row.SourceText)
		if detected == "" || language.IsUnknown(detected) {
			continue
		}
		updates = append(updates, update{tweetID: row.TweetID, field: row.Field, lang: detected})
	}
	fmt.Printf("language updates: %d\n", len(updates))
	if !apply || len(updates) == 0 {
		return nil
	}
	return database.WithWrite(func(tx *sql.Tx) error {
		for _, u := range updates {
			col := "lang"
			if u.field == "quote" {
				col = "quote_lang"
			}
			if _, err := tx.Exec(`UPDATE feed_items SET `+col+` = ? WHERE tweet_id = ?`, u.lang, u.tweetID); err != nil {
				return err
			}
		}
		return nil
	})
}

func runExport(ctx context.Context, database *db.DB, target string, skip []string, limit int, outPath, mapPath string) error {
	rows, err := loadCatchupCandidates(ctx, database, target, skip, limit)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil && filepath.Dir(outPath) != "." {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(mapPath), 0o755); err != nil && filepath.Dir(mapPath) != "." {
		return err
	}
	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = outFile.Close()
	}()
	mapFile, err := os.Create(mapPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = mapFile.Close()
	}()
	outEnc := json.NewEncoder(outFile)
	mapEnc := json.NewEncoder(mapFile)
	exported := 0
	for _, row := range rows {
		if !shouldExportCandidate(row, target, skip) {
			continue
		}
		sourceLang := effectiveCandidateLang(row)
		protected, tokens := protect(row.SourceText)
		if strings.TrimSpace(protected) == "" {
			continue
		}
		rowID := fmt.Sprintf("row_%06d", exported+1)
		contextHint := ""
		if row.Field == "body" && row.QuoteBodyText != "" {
			contextHint = protectContext(row.QuoteBodyText)
		} else if row.Field == "quote" && row.BodyText != "" {
			contextHint = protectContext(row.BodyText)
		}
		if err := outEnc.Encode(exportRow{
			RowID:      rowID,
			Field:      row.Field,
			SourceLang: sourceLang,
			TargetLang: target,
			Text:       protected,
			Context:    contextHint,
			Tokens:     tokens,
		}); err != nil {
			return err
		}
		if err := mapEnc.Encode(mapRow{
			RowID:      rowID,
			TweetID:    row.TweetID,
			Field:      row.Field,
			SourceLang: sourceLang,
			TargetLang: target,
			Tokens:     tokens,
		}); err != nil {
			return err
		}
		exported++
	}
	fmt.Printf("exported rows: %d\nbatch: %s\nmap: %s\n", exported, outPath, mapPath)
	return nil
}

func runImport(database *db.DB, inPath, mapPath string) error {
	mapping, err := readMapRows(mapPath)
	if err != nil {
		return err
	}
	file, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	scanner := bufio.NewScanner(file)
	imported := 0
	for scanner.Scan() {
		var row importRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return err
		}
		meta, ok := mapping[row.RowID]
		if !ok {
			return fmt.Errorf("unknown row_id %q", row.RowID)
		}
		text := strings.TrimSpace(row.TranslatedText)
		if text == "" {
			return fmt.Errorf("empty translated_text for %s", row.RowID)
		}
		if !preservesTokens(text, meta.Tokens) {
			return fmt.Errorf("translated_text for %s dropped a protected token", row.RowID)
		}
		if err := database.SetTranslation(meta.TweetID, meta.Field, meta.SourceLang, meta.TargetLang, text); err != nil {
			return err
		}
		imported++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	fmt.Printf("imported rows: %d\n", imported)
	return nil
}

func loadCatchupCandidates(ctx context.Context, database *db.DB, target string, skip []string, limit int) ([]candidateRow, error) {
	var rows []candidateRow
	err := database.WithRead(func(conn *sql.DB) error {
		query := `
			SELECT tweet_id, field, source_text, source_lang, body_text, quote_body_text
			FROM (
				SELECT f.tweet_id, 'body' AS field, COALESCE(f.body_text,'') AS source_text,
				       LOWER(TRIM(COALESCE(f.lang,''))) AS source_lang,
				       COALESCE(f.body_text,'') AS body_text,
				       COALESCE(f.quote_body_text,'') AS quote_body_text,
				       f.published_at AS published_at
				FROM feed_items f
				LEFT JOIN translations tr ON tr.tweet_id = f.tweet_id AND tr.field = 'body' AND tr.target_lang = ?
				WHERE tr.tweet_id IS NULL AND TRIM(COALESCE(f.body_text,'')) != ''
				UNION ALL
				SELECT f.tweet_id, 'quote' AS field, COALESCE(f.quote_body_text,'') AS source_text,
				       LOWER(TRIM(COALESCE(f.quote_lang,''))) AS source_lang,
				       COALESCE(f.body_text,'') AS body_text,
				       COALESCE(f.quote_body_text,'') AS quote_body_text,
				       f.published_at AS published_at
				FROM feed_items f
				LEFT JOIN translations tr ON tr.tweet_id = f.tweet_id AND tr.field = 'quote' AND tr.target_lang = ?
				WHERE tr.tweet_id IS NULL AND TRIM(COALESCE(f.quote_body_text,'')) != ''
			)
			ORDER BY published_at DESC, tweet_id DESC, field ASC
			LIMIT ?`
		sqlRows, err := conn.QueryContext(ctx, query, target, target, limit)
		if err != nil {
			return err
		}
		defer func() {
			_ = sqlRows.Close()
		}()
		for sqlRows.Next() {
			var row candidateRow
			if err := sqlRows.Scan(&row.TweetID, &row.Field, &row.SourceText, &row.SourceLang, &row.BodyText, &row.QuoteBodyText); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return sqlRows.Err()
	})
	if err != nil {
		return nil, err
	}
	filtered := rows[:0]
	for _, row := range rows {
		if shouldConsiderCandidate(row, target, skip) {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

func shouldConsiderCandidate(row candidateRow, target string, skip []string) bool {
	lang := effectiveCandidateLang(row)
	if lang == "" || language.IsUnknown(lang) {
		return false
	}
	if language.Matches(lang, target) {
		return false
	}
	for _, skipped := range skip {
		if language.Matches(lang, skipped) {
			return false
		}
	}
	return true
}

func shouldExportCandidate(row candidateRow, target string, skip []string) bool {
	return shouldConsiderCandidate(row, target, skip)
}

func effectiveCandidateLang(row candidateRow) string {
	lang := row.SourceLang
	if language.IsUnknown(lang) {
		lang = xfeed.DetectLang(row.SourceText)
	}
	return strings.ToLower(strings.TrimSpace(lang))
}

func protect(text string) (string, []string) {
	var tokens []string
	protected := tokenRe.ReplaceAllStringFunc(text, func(token string) string {
		placeholder := fmt.Sprintf("{{%d}}", len(tokens))
		tokens = append(tokens, token)
		return placeholder
	})
	return strings.TrimSpace(protected), tokens
}

func protectContext(text string) string {
	protected, _ := protect(text)
	runes := []rune(protected)
	if len(runes) > 500 {
		return strings.TrimSpace(string(runes[:500]))
	}
	return protected
}

func preservesTokens(text string, tokens []string) bool {
	for i, token := range tokens {
		placeholder := fmt.Sprintf("{{%d}}", i)
		if strings.Contains(text, placeholder) || strings.Contains(text, token) {
			continue
		}
		return false
	}
	return true
}

func readMapRows(path string) (map[string]mapRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	out := map[string]mapRow{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row mapRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, err
		}
		out[row.RowID] = row
	}
	return out, scanner.Err()
}

func splitCSV(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func printCounts(label string, counts map[string]int) {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Println(label + ":")
	for _, key := range keys {
		fmt.Printf("  %s: %d\n", key, counts[key])
	}
}

func defaultDBPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "igloo", "igloo.db")
	}
	return "igloo.db"
}

func defaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "igloo")
	}
	return "."
}
