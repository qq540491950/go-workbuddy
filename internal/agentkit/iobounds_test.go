package agentkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestReadBoundedFileTruncatesAtRuneBoundary: a file larger than the read
// cap must come back truncated with a marker, without ever loading the whole
// file into memory and without splitting a UTF-8 sequence.
func TestReadBoundedFileTruncatesAtRuneBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.txt")
	big := strings.Repeat("汉", 40*1024) // 120KB of UTF-8, cap is 64KB
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	content, truncated, err := ReadBoundedFile(path, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(content) {
		t.Fatal("truncated content is not valid UTF-8")
	}
	if !strings.HasSuffix(content, "\n...[truncated]") {
		t.Fatalf("truncation marker missing, tail: %q", content[len(content)-30:])
	}
	if len(content) > 64*1024+len("\n...[truncated]") {
		t.Fatalf("content exceeds cap: %d bytes", len(content))
	}
}

func TestReadBoundedFileSmallAndExactLimit(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(small, []byte("你好，工作区"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, truncated, err := ReadBoundedFile(small, 64*1024)
	if err != nil || truncated || content != "你好，工作区" {
		t.Fatalf("small file: content=%q truncated=%v err=%v", content, truncated, err)
	}

	exact := filepath.Join(dir, "exact.txt")
	payload := strings.Repeat("a", 64*1024)
	if err := os.WriteFile(exact, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	content, truncated, err = ReadBoundedFile(exact, 64*1024)
	if err != nil || truncated || content != payload {
		t.Fatalf("exact-limit file must not truncate: truncated=%v len=%d err=%v", truncated, len(content), err)
	}
}

// TestWriteBoundedFileRejectsOversize: the write tool must refuse oversized
// payloads up front instead of letting a runaway generation fill the disk.
func TestWriteBoundedFileRejectsOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := WriteBoundedFile(path, strings.Repeat("x", 6<<20+1), 6<<20); err == nil {
		t.Fatal("expected oversize rejection")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rejected write must not create the file")
	}
	if err := WriteBoundedFile(path, "正常内容", 6<<20); err != nil {
		t.Fatalf("bounded write failed: %v", err)
	}
}
