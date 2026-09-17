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
