package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotatingWriterRotates: exceeding maxBytes moves the file to .old and
// subsequent writes land in a fresh app.log — long-resident tray apps used to
// grow app.log without bound (rotation ran only at startup).
func TestRotatingWriterRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w, err := newRotatingWriter(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	// Write past the cap: some writes go into the first generation, the rest
	// into the rotated one.
	for i := 0; i < 20; i++ {
		if _, err := w.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Fatalf("rotation did not produce %s.old: %v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || len(data) > 64+10 {
		t.Fatalf("fresh log size = %d", len(data))
	}
	oldData, err := os.ReadFile(path + ".old")
	if err != nil || !strings.HasPrefix(string(oldData), "0123456789") {
		t.Fatalf("old log missing first generation: err=%v", err)
	}
}
