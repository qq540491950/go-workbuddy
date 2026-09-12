package agentkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchKnowledge(t *testing.T) {
	ws := t.TempDir()
	kb := filepath.Join(ws, "knowledge")
	if err := os.MkdirAll(kb, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(kb, "oncall.md"), []byte("# 值班手册\n\n- 一级故障响应时间：5 分钟\n- 升级路径：值班 -> SRE 负责人\n- 电话：1010"), 0o644)
	_ = os.WriteFile(filepath.Join(kb, "styles.txt"), []byte("写作风格：简洁、量化、避免空话。\n周报需包含进度百分比。"), 0o644)
	_ = os.WriteFile(filepath.Join(kb, "ignored.exe"), []byte("nope"), 0o644)

	res, err := searchKnowledge(ws, "值班 升级")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "oncall.md") || !strings.Contains(res, "升级路径") {
		t.Fatalf("search result missing expected content:\n%s", res)
	}
	if strings.Contains(res, "styles.txt") {
		t.Fatalf("irrelevant file should not match:\n%s", res)
	}

	// No match message
	res, err = searchKnowledge(ws, "quantum-entanglement")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "没有找到") {
		t.Fatalf("no-match message missing:\n%s", res)
	}

	// Missing knowledge dir is a friendly message, not an error.
	ws2 := t.TempDir()
	res, err = searchKnowledge(ws2, "anything")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "知识库为空") {
		t.Fatalf("empty-kb message missing:\n%s", res)
	}
}
