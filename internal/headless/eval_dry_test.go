package headless

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func TestEvalDryRun(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", "eval/run.sh", "--dry-run")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run.sh: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "dry-run: 15 tasks") || !strings.Contains(text, "edit_failure_rate") {
		t.Fatalf("output:\n%s", text)
	}

	dir := t.TempDir()
	pred := filepath.Join(dir, "predictions.jsonl")
	py := exec.Command("python3", "eval/swebench.py", "--dry-run", "--limit", "2", "--output", pred)
	py.Dir = root
	out, err = py.CombinedOutput()
	if err != nil {
		t.Fatalf("swebench: %v\n%s", err, out)
	}
	body, err := os.ReadFile(pred)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"model_patch": ""`) || !strings.Contains(lines[0], "instance_id") {
		t.Fatalf("predictions:\n%s", body)
	}

	base := exec.Command("python3", "eval/baseline.py", "--dry-run", "--limit", "2")
	base.Dir = root
	out, err = base.CombinedOutput()
	if err != nil {
		t.Fatalf("baseline: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "dry-run: 2 instances") || !strings.Contains(string(out), "mini-swe-agent") {
		t.Fatalf("baseline output:\n%s", out)
	}
}

func TestHiddenCheckPassesWhenFixed(t *testing.T) {
	root := repoRoot(t)
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "eval/tasks/01-hello/check.sh"))
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "WORKSPACE="+work)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
}
