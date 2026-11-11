package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIncludesAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	user := filepath.Join(home, "xdg", "sigma", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(user), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte("be brief"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("run the tests"), 0o644); err != nil {
		t.Fatal(err)
	}

	sections, err := LoadAgents(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 2 {
		t.Fatalf("sections: %+v", sections)
	}
	text := Build(ws, "ask", sections)
	if !strings.Contains(text, "be brief") || !strings.Contains(text, "run the tests") {
		t.Fatalf("prompt missing AGENTS.md:\n%s", text)
	}
	if !strings.Contains(text, "old_string") || !strings.Contains(text, ws) {
		t.Fatalf("prompt missing tool guidance:\n%s", text)
	}
	plan := Build(ws, "plan", nil)
	if !strings.Contains(plan, "read-only") {
		t.Fatal(plan)
	}
}
