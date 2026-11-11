package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oasis1i/sigma/internal/provider"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Create(dir, map[string]string{"model": "m", "base_url": "http://example.test/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.ID, "/") {
		t.Fatal(s.ID)
	}
	msgs := []provider.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "fix the test"},
		{Role: "assistant", Content: "editing", ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "edit_file", Arguments: `{"path":"a.go"}`,
		}}},
		{Role: "tool", Name: "edit_file", ToolCallID: "c1", Content: "edited a.go"},
	}
	for _, m := range msgs {
		if err := s.AppendMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendApproval(Approval{
		Tool: "bash", Arg: "go test", Action: "allow", Reason: "user approved (always)", Remember: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "api_key") || strings.Contains(string(body), "sk-") {
		t.Fatalf("log leaked a secret:\n%s", body)
	}

	got, err := Open(dir, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	loaded, err := got.Messages()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(msgs) {
		t.Fatalf("messages: got %d want %d", len(loaded), len(msgs))
	}
	for i := range msgs {
		if loaded[i].Role != msgs[i].Role || loaded[i].Content != msgs[i].Content || loaded[i].ToolCallID != msgs[i].ToolCallID {
			t.Fatalf("message %d: %+v", i, loaded[i])
		}
	}
	if len(loaded[2].ToolCalls) != 1 || loaded[2].ToolCalls[0].Name != "edit_file" {
		t.Fatalf("tool calls: %+v", loaded[2].ToolCalls)
	}
	remembered, err := got.RememberedTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(remembered) != 1 || remembered[0] != "bash" {
		t.Fatalf("remembered: %v", remembered)
	}

	// A unique prefix resumes the same file.
	prefix := s.ID[:len(s.ID)-2]
	byPrefix, err := Open(dir, prefix)
	if err != nil {
		t.Fatal(err)
	}
	byPrefix.Close()
	if byPrefix.ID != s.ID {
		t.Fatalf("prefix opened %s", byPrefix.ID)
	}
}

func TestContinueLatestAndAmbiguousPrefix(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "20260101-000000-aaaaaa.jsonl")
	newer := filepath.Join(dir, "20260101-000001-bbbbbb.jsonl")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte(`{"ts":"2026-01-01T00:00:00Z","type":"meta","meta":{"id":"x"}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}
	latest, err := OpenLatest(dir)
	if err != nil {
		t.Fatal(err)
	}
	latest.Close()
	if latest.ID != "20260101-000001-bbbbbb" {
		t.Fatalf("latest: %s", latest.ID)
	}
	if _, err := Open(dir, "20260101"); err == nil || !strings.Contains(err.Error(), "matches") {
		t.Fatalf("ambiguous prefix: %v", err)
	}
	exact, err := Open(dir, "20260101-000000-aaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	exact.Close()
	if _, err := Open(dir, "../secret"); err == nil {
		t.Fatal("path traversal should fail")
	}
}
