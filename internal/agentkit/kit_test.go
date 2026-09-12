package agentkit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeWorkspacePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		rel    string
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

func TestSanitizeAgentName(t *testing.T) {
	cases := map[string]string{
		"周报助手":      "agent_", // non-ascii only -> fallback suffix needed; starts with agent_
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
