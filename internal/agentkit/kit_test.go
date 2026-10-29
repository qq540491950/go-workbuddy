package agentkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeWorkspacePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		rel     string
		wantErr bool
	}{
		{"notes.txt", false},
		{"sub/notes.txt", false},
		{".", false},
		{"../escape.txt", true},
		{"a/../../escape.txt", true},
	}
	for _, c := range cases {
		got, err := safeWorkspacePath(root, c.rel)
		if c.wantErr {
			if err == nil {
				t.Errorf("rel %q: expected error, got %q", c.rel, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("rel %q: unexpected error %v", c.rel, err)
		}
	}
}

func TestEmptyWorkspaceRejected(t *testing.T) {
	if _, err := safeWorkspacePath("", "x.txt"); err == nil {
		t.Fatal("expected error for empty workspace")
	}
}

// TestSafeWorkspacePathBlocksSymlinkEscape: a symlink inside the workspace
// must not become a way out of it. The lexical prefix check alone passes such
// a path, so this pins the resolved-path check.
func TestSafeWorkspacePathBlocksSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := safeWorkspacePath(root, "leak.txt"); err == nil {
		t.Fatal("symlink pointing outside the workspace was allowed")
	}

	// A symlinked directory escaping the workspace is equally blocked.
	if err := os.Symlink(outside, filepath.Join(root, "outdir")); err != nil {
		t.Fatal(err)
	}
	if _, err := safeWorkspacePath(root, "outdir/secret.txt"); err == nil {
		t.Fatal("path through an escaping symlinked dir was allowed")
	}

	// A symlink that stays inside remains usable.
	inner := filepath.Join(root, "inner.txt")
	if err := os.WriteFile(inner, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inner, filepath.Join(root, "inner-link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := safeWorkspacePath(root, "inner-link.txt"); err != nil {
		t.Fatalf("in-workspace symlink rejected: %v", err)
	}
}

func TestSanitizeAgentName(t *testing.T) {
	cases := map[string]string{
		"周报助手":        "agent_", // non-ascii only -> fallback suffix needed; starts with agent_
		"Weekly Bot!": "weekly_bot",
		"user":        "user_agent",
	}
	for in, wantPrefix := range cases {
		got := sanitizeAgentName(in)
		if got == "" || got == "user" {
			t.Errorf("sanitize(%q) = %q, invalid", in, got)
		}
		if wantPrefix == "agent_" && got != "agent_" && len(got) < 6 {
			t.Errorf("sanitize(%q) = %q, expected fallback form", in, got)
		}
	}
	if sanitizeAgentName("Weekly Bot!") != "weekly_bot" {
		t.Error("unexpected sanitize result")
	}
}

func TestApplyPromptVars(t *testing.T) {
	out := applyPromptVars("今天 {{date}} {{time}}，工作区 {{workspace}}。", "/tmp/ws")
	if !strings.HasPrefix(out, "今天 ") || !strings.Contains(out, "，工作区 /tmp/ws。") {
		t.Fatalf("vars unresolved: %q", out)
	}
	if strings.Contains(out, "{{") {
		t.Fatalf("leftover placeholders: %q", out)
	}
	if got := applyPromptVars("no vars", ""); got != "no vars" {
		t.Fatalf("plain prompt mutated: %q", got)
	}
}
