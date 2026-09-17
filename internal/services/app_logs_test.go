package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecentLogs(t *testing.T) {
	app := &AppService{S: &Services{}}
	logPath := filepath.Join(t.TempDir(), "app.log")

	// Missing file: friendly empty message.
	app.SetLogPath(logPath)
	lines, err := app.RecentLogs(50)
	if err != nil || len(lines) != 1 {
		t.Fatalf("missing file should yield placeholder: %v %v", lines, err)
	}

	// Tail semantics: 300 lines written, last 50 returned.
	var sb strings.Builder
	for i := 1; i <= 300; i++ {
		sb.WriteString("line-" + itoaTest(i) + "\n")
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err = app.RecentLogs(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 50 {
		t.Fatalf("expected 50 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "line-300") {
		t.Fatalf("tail wrong: %v", lines[len(lines)-1])
	}
	if strings.Contains(strings.Join(lines, "\n"), "line-250") {
		t.Fatal("tail included lines beyond the window")
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	d := ""
	for n > 0 {
		d = string(rune('0'+n%10)) + d
		n /= 10
	}
	return d
}

// TestStorageStatsAndVacuum: sizes are reported from real files and VACUUM
// runs without error on the live sessions database.
func TestStorageStatsAndVacuum(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	kPath := filepath.Join(dir, "knowledge.index.db")
	db, _, err := newTestSessionService(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	app := &AppService{S: &Services{}}
	app.SetStoragePaths(dbPath, kPath, db)

	info := app.StorageStats()
	if info.SessionsDBBytes <= 0 {
		t.Fatalf("sessions db size missing: %+v", info)
	}
	if info.KnowledgeDBBytes != -1 {
		t.Fatalf("absent knowledge db should report -1: %+v", info)
	}
	if err := os.WriteFile(kPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info = app.StorageStats(); info.KnowledgeDBBytes != 1 {
		t.Fatalf("knowledge db size wrong: %+v", info)
	}
	if err := app.VacuumSessions(); err != nil {
		t.Fatalf("vacuum failed: %v", err)
	}
	// Database still usable afterwards: count query works.
	if err := db.Exec("SELECT count(*) FROM sqlite_master").Error; err != nil {
		t.Fatalf("db unusable after vacuum: %v", err)
	}
}

// TestRecentToolAuditTail: the tool audit viewer reads the JSONL file via the
// same tail semantics (last N lines, placeholder when missing).
func TestRecentToolAuditTail(t *testing.T) {
	app := &AppService{S: &Services{}}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	app.SetToolAuditPath(auditPath)

	lines, err := app.RecentToolAudit(10)
	if err != nil || len(lines) != 1 {
		t.Fatalf("missing audit should yield placeholder: %v %v", lines, err)
	}

	if err := os.WriteFile(auditPath, []byte("r1\nr2\nr3\nr4\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	lines, err = app.RecentToolAudit(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "r3" || lines[1] != "r4" {
		t.Fatalf("tail wrong: %v", lines)
	}
}
