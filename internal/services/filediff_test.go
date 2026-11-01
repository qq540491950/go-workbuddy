package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-workbuddy/internal/config"
)

// fileDiffFixture wires an artifact service with a session whose workspace is
// tasks/sess_diff so FileDiff / baseline helpers can be exercised end to end.
func fileDiffFixture(t *testing.T) (*ArtifactService, string) {
	t.Helper()
	svc, ws := newArtifactFixture(t)
	sessionDir := filepath.Join(ws, "tasks", "sess_diff")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := svc.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = []config.ChatSession{{ID: "sess_diff", WorkspaceDir: sessionDir}}
	}); err != nil {
		t.Fatal(err)
	}
	return svc, sessionDir
}

func writeWSFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustSplit(t *testing.T, text string) []string {
	t.Helper()
	return splitDiffLines(text)
}

func TestSplitDiffLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a\nb", []string{"a", "b"}},
		{"a\n", []string{"a"}},
		{"a\n\n", []string{"a", ""}},
		{"\n", []string{""}},
		{"a\r\nb\r\n", []string{"a", "b"}},
	}
	for _, tc := range cases {
		got := splitDiffLines(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("split(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("split(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
	_ = mustSplit
}

func TestLineDiffScenarios(t *testing.T) {
	cases := []struct {
		name           string
		oldText        string
		newText        string
		wantAdded      int
		wantRemoved    int
		wantHunks      int
		wantCtxPerHunk int // total context lines across all hunks
	}{
		{"identical", "a\nb\nc\n", "a\nb\nc\n", 0, 0, 0, 0},
		// 3-line files only have one context line available on each side.
		{"insert middle", "a\nb\nc\n", "a\nX\nc\n", 1, 1, 1, 2},
		{"pure insert", "a\nc\n", "a\nb\nc\n", 1, 0, 1, 2},
		{"pure delete", "a\nb\nc\n", "a\nc\n", 0, 1, 1, 2},
		{"append", "a\n", "a\nb\nc\nd\n", 3, 0, 1, 1},
		{"prepend", "d\n", "a\nb\nd\n", 2, 0, 1, 1},
		{"from empty", "", "a\nb\n", 2, 0, 1, 0},
		{"to empty", "a\nb\n", "", 0, 2, 1, 0},
		{"far apart two hunks", "1\n2\n3\n4\n5\n6\n7\n8\n9\nA\nB\nC\nD\nE\nF\n",
			"1\nX\n3\n4\n5\n6\n7\n8\n9\nA\nB\nC\nD\nY\nF\n", 2, 2, 2, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := mustSplit(t, tc.oldText), mustSplit(t, tc.newText)
			hunks, added, removed, truncated := groupHunks(lineDiff(a, b), a, b)
			if added != tc.wantAdded || removed != tc.wantRemoved {
				t.Fatalf("added/removed = %d/%d, want %d/%d", added, removed, tc.wantAdded, tc.wantRemoved)
			}
			if len(hunks) != tc.wantHunks {
				t.Fatalf("hunks = %d, want %d (%+v)", len(hunks), tc.wantHunks, hunks)
			}
			if truncated {
				t.Fatal("unexpected truncation")
			}
			ctx := 0
			for _, h := range hunks {
				for _, l := range h.Lines {
					if l.Type == DiffLineContext {
						ctx++
					}
				}
			}
			if ctx != tc.wantCtxPerHunk {
				t.Fatalf("context lines = %d, want %d", ctx, tc.wantCtxPerHunk)
			}
			// Context rows must reconstruct both sources through their numbers.
			for _, h := range hunks {
				for _, l := range h.Lines {
					switch l.Type {
					case DiffLineContext:
						if a[l.OldNo-1] != l.Text || b[l.NewNo-1] != l.Text {
							t.Fatalf("context row %+v does not match sources", l)
						}
					case DiffLineDel:
						if a[l.OldNo-1] != l.Text {
							t.Fatalf("del row %+v does not match source", l)
						}
					case DiffLineAdd:
						if b[l.NewNo-1] != l.Text {
							t.Fatalf("add row %+v does not match source", l)
						}
					}
				}
			}
		})
	}
}

// 8000 行大文件两处孤立修改：前缀/后缀裁剪后中段超出 DP 上限，走分块路径；
// 结果必须是两个局部 hunk，而不是 4000 行整段替换。
func TestLineDiffLargeMiddleStaysLocalized(t *testing.T) {
	mk := func(mutate func(i int) string) []string {
		lines := make([]string, 8000)
		for i := range lines {
			lines[i] = mutate(i)
		}
		return lines
	}
	a := mk(func(i int) string { return fmt.Sprintf("line-%d", i+1) })
	b := mk(func(i int) string {
		if i == 2499 || i == 5499 {
			return fmt.Sprintf("changed-%d", i+1)
		}
		return fmt.Sprintf("line-%d", i+1)
	})
	hunks, added, removed, truncated := groupHunks(lineDiff(a, b), a, b)
	if added != 2 || removed != 2 {
		t.Fatalf("added/removed = %d/%d, want 2/2", added, removed)
	}
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2 localized hunks", len(hunks))
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	for _, h := range hunks {
		if len(h.Lines) != 8 { // 3 context + del + add + 3 context
			t.Fatalf("hunk size = %d, want 8", len(h.Lines))
		}
	}
	if hunks[0].OldStart != 2497 || hunks[1].OldStart != 5497 {
		t.Fatalf("hunk starts = %d/%d, want 2497/5497", hunks[0].OldStart, hunks[1].OldStart)
	}
}

func TestGroupHunksTruncationCap(t *testing.T) {
	mk := func(marker string) []string {
		lines := make([]string, 8000)
		for i := range lines {
			if i%5 == 0 {
				lines[i] = fmt.Sprintf("%s-%d", marker, i)
			} else {
				lines[i] = fmt.Sprintf("line-%d", i)
			}
		}
		return lines
	}
	a, b := mk("old"), mk("new")
	_, _, _, truncated := groupHunks(lineDiff(a, b), a, b)
	if !truncated {
		t.Fatal("expected truncation for very large diff")
	}
}

func TestFileDiffAddedModifiedDeletedUnchanged(t *testing.T) {
	svc, dir := fileDiffFixture(t)
	writeWSFile(t, dir, "mod.txt", "one\ntwo\nthree\n")
	writeWSFile(t, dir, "del.txt", "gone-1\ngone-2\n")
	writeWSFile(t, dir, "same.txt", "stable\n")
	if _, err := svc.TaskWorkspaceSnapshot("sess_diff"); err != nil {
		t.Fatal(err)
	}
	writeWSFile(t, dir, "mod.txt", "one\nTWO-edited\nthree\nfour\n")
	writeWSFile(t, dir, "new.txt", "fresh lines\nhere\n")
	if err := os.Remove(filepath.Join(dir, "del.txt")); err != nil {
		t.Fatal(err)
	}

	mod, err := svc.FileDiff("sess_diff", "mod.txt")
	if err != nil {
		t.Fatal(err)
	}
	if mod.Status != ChangeModified || mod.Unsupported != "" {
		t.Fatalf("mod = %+v", mod)
	}
	if mod.AddedLines != 2 || mod.RemovedLines != 1 {
		t.Fatalf("mod counts = %d/%d, want 2/1", mod.AddedLines, mod.RemovedLines)
	}
	if len(mod.Hunks) != 1 || len(mod.Hunks[0].Lines) == 0 {
		t.Fatalf("mod hunks = %+v", mod.Hunks)
	}
	if mod.OldSize == 0 || mod.NewSize == 0 {
		t.Fatalf("mod sizes = %d/%d", mod.OldSize, mod.NewSize)
	}

	added, err := svc.FileDiff("sess_diff", "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if added.Status != ChangeAdded || added.AddedLines != 2 || added.RemovedLines != 0 {
		t.Fatalf("added = %+v", added)
	}
	for _, h := range added.Hunks {
		for _, l := range h.Lines {
			if l.Type != DiffLineAdd || l.OldNo != 0 {
				t.Fatalf("added rows must be pure additions: %+v", l)
			}
		}
	}

	deleted, err := svc.FileDiff("sess_diff", "del.txt")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Status != ChangeDeleted || deleted.RemovedLines != 2 {
		t.Fatalf("deleted = %+v", deleted)
	}
	var delTexts []string
	for _, h := range deleted.Hunks {
		for _, l := range h.Lines {
			delTexts = append(delTexts, l.Text)
		}
	}
	if strings.Join(delTexts, "|") != "gone-1|gone-2" {
		t.Fatalf("deleted content = %v", delTexts)
	}

	unchanged, err := svc.FileDiff("sess_diff", "same.txt")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "" || len(unchanged.Hunks) != 0 {
		t.Fatalf("unchanged = %+v", unchanged)
	}
}

func TestFileDiffUnsupportedCases(t *testing.T) {
	svc, dir := fileDiffFixture(t)
	big := strings.Repeat("内容行 tail\n", 30*1024) // >256KB snapshot cap, <2MB diff cap
	if len(big) <= diffContentFileMax || len(big) >= diffMaxFile {
		t.Fatalf("fixture size %d outside (256KB, 2MB)", len(big))
	}
	writeWSFile(t, dir, "huge.txt", big)
	writeWSFile(t, dir, "bin.dat", "\x00\x01\x02binary")
	writeWSFile(t, dir, "gone-bin.dat", "\x00\x01data")
	oversize := strings.Repeat("x", diffMaxFile+1)
	writeWSFile(t, dir, "oversize.txt", oversize)
	if _, err := svc.TaskWorkspaceSnapshot("sess_diff"); err != nil {
		t.Fatal(err)
	}
	// 基线建立后：修改文本/二进制文件，删除基线即二进制的文件，追加超大文件。
	writeWSFile(t, dir, "huge.txt", big+"追加行\n")
	writeWSFile(t, dir, "bin.dat", "\x00\x09changed")
	if err := os.Remove(filepath.Join(dir, "gone-bin.dat")); err != nil {
		t.Fatal(err)
	}
	writeWSFile(t, dir, "oversize.txt", oversize+"y")

	huge, err := svc.FileDiff("sess_diff", "huge.txt")
	if err != nil {
		t.Fatal(err)
	}
	if huge.Unsupported == "" || !strings.Contains(huge.Unsupported, "基线未保存") {
		t.Fatalf("huge unsupported = %+v", huge)
	}
	if huge.Status != ChangeModified || len(huge.Hunks) != 0 {
		t.Fatalf("huge = %+v", huge)
	}

	bin, err := svc.FileDiff("sess_diff", "bin.dat")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bin.Unsupported, "二进制") {
		t.Fatalf("binary unsupported = %+v", bin)
	}

	// 删除一个基线时就是二进制的文件：无法展示删除详情。
	gone, err := svc.FileDiff("sess_diff", "gone-bin.dat")
	if err != nil {
		t.Fatal(err)
	}
	if gone.Status != ChangeDeleted || !strings.Contains(gone.Unsupported, "基线未保存") {
		t.Fatalf("gone binary = %+v", gone)
	}

	// 超过 diff 上限（2MB）的文本文件。
	over, err := svc.FileDiff("sess_diff", "oversize.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(over.Unsupported, "上限") {
		t.Fatalf("oversize = %+v", over)
	}
}

func TestFileDiffRejectsUnsafePathsAndUnknownSession(t *testing.T) {
	svc, dir := fileDiffFixture(t)
	writeWSFile(t, dir, "a.txt", "x")
	if _, err := svc.TaskWorkspaceSnapshot("sess_diff"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FileDiff("sess_diff", "../escape.txt"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := svc.FileDiff("sess_none", "a.txt"); err == nil {
		t.Fatal("expected unknown-session error")
	}
	if _, err := svc.FileDiff("sess_diff", "missing.txt"); err == nil {
		t.Fatal("expected missing-file error")
	}
}

func TestBaselineContentSnapshotAndCleanup(t *testing.T) {
	svc, dir := fileDiffFixture(t)
	writeWSFile(t, dir, "keep.txt", "keep me\n")
	writeWSFile(t, dir, "empty.txt", "")
	baseline, err := ensureTaskBaseline(svc.S.Store, "sess_diff", dir)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Files["keep.txt"].Content == "" {
		t.Fatal("text file should get a content snapshot")
	}
	if baseline.Files["empty.txt"].Content != "" {
		t.Fatal("empty file should not get a content snapshot")
	}
	if _, ok := readTaskContent(svc.S.Store, "sess_diff", baseline.Files["keep.txt"].Content); !ok {
		t.Fatal("content file missing on disk")
	}
	contentDir := taskContentDir(svc.S.Store, "sess_diff")
	if st, err := os.Stat(contentDir); err != nil || !st.IsDir() {
		t.Fatalf("content dir missing: %v", err)
	}

	// 重建基线必须清掉旧内容再重拍。
	writeWSFile(t, dir, "keep.txt", "replaced\n")
	if _, err := resetTaskBaseline(svc.S.Store, "sess_diff", dir); err != nil {
		t.Fatal(err)
	}
	baseline2, err := ensureTaskBaseline(svc.S.Store, "sess_diff", dir)
	if err != nil {
		t.Fatal(err)
	}
	data, ok := readTaskContent(svc.S.Store, "sess_diff", baseline2.Files["keep.txt"].Content)
	if !ok || string(data) != "replaced\n" {
		t.Fatalf("reset content = %q (ok=%v), want replaced", data, ok)
	}

	// 删除会话基线时内容目录一并清理。
	removeTaskBaseline(svc.S.Store, "sess_diff")
	if _, err := os.Stat(contentDir); !os.IsNotExist(err) {
		t.Fatalf("content dir should be removed, got %v", err)
	}
}
