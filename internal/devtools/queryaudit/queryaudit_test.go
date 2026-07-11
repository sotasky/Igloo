package queryaudit

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	igloodb "github.com/screwys/igloo/internal/db"
)

func TestRunReportsHotPathPlans(t *testing.T) {
	dbPath := createQueryAuditProductionFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"-db", dbPath, "-limit", "5"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"db: " + dbPath,
		"query_audit:",
		"feed_snapshot_page:",
		"videos_shorts_page:",
		"android_sync_media_videos:",
		"asset_download_claim_candidates:",
		"channel_search:",
		"video_search:",
		"elapsed_ms:",
		"rows:",
		"plan:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestReadReportCollectsPlans(t *testing.T) {
	dbPath := createQueryAuditProductionFixture(t)

	report, err := ReadReport(dbPath, Options{Limit: 5, Search: "Sample"})
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if report.DBPath != dbPath {
		t.Fatalf("DBPath = %q, want %q", report.DBPath, dbPath)
	}
	if len(report.Probes) != len(probeSpecs()) {
		t.Fatalf("probe count = %d, want %d", len(report.Probes), len(probeSpecs()))
	}
	for _, probe := range report.Probes {
		if probe.Name == "" {
			t.Fatalf("probe without name: %+v", probe)
		}
		if probe.Error != "" {
			t.Fatalf("probe %s errored: %s", probe.Name, probe.Error)
		}
		if len(probe.Plan) == 0 {
			t.Fatalf("probe %s missing query plan", probe.Name)
		}
	}
}

func TestParseOptionsRejectsUnknownProbe(t *testing.T) {
	if _, err := parseOptions([]string{"-probe", "missing"}); err == nil {
		t.Fatal("parseOptions accepted unknown probe")
	}
}

func createQueryAuditProductionFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "igloo.db")
	d, err := igloodb.OpenPath(dbPath, tmp)
	if err != nil {
		t.Fatalf("open production fixture db: %v", err)
	}
	defer func() {
		_ = d.Close()
	}()

	stmts := []string{
		`INSERT INTO channels (channel_id, source_id, name, platform)
		 VALUES ('tiktok_sample_channel', 'sample_channel', 'Sample Channel', 'tiktok')`,
		`INSERT INTO channel_follows (channel_id, followed_at)
			 VALUES ('tiktok_sample_channel', 1000)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		 VALUES ('sample_video_1', 'tiktok_sample_channel', 'tiktok_video', 'Sample Video', 1500)`,
	}
	for _, stmt := range stmts {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("exec production fixture statement %q: %v", stmt, err)
		}
	}
	return dbPath
}
