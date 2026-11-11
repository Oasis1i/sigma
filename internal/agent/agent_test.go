package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/sandbox"
	"github.com/Oasis1i/sigma/internal/session"
	"github.com/Oasis1i/sigma/internal/tools"
)

func newTestAgent(t *testing.T, mode string, rules []approval.Rule, script *provider.Scripted) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	policy, err := approval.New(mode, rules)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := session.Create(t.TempDir(), map[string]string{"model": "fake"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	ag := &Agent{
		Client: script,
		Registry: tools.NewRegistry(&tools.Env{
			Workspace:   dir,
			Runner:      &sandbox.Local{Dir: dir},
			BashTimeout: 5 * time.Second,
		}),
		Policy:   policy,
		Session:  sess,
		MaxSteps: 10,
	}
	return ag, dir
}

func TestLoopRunsToolThenStops(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "write_file", Arguments: `{"path":"hello.txt","content":"hi\n"}`,
		}}},
		{Content: "created hello.txt"},
	}}
	ag, dir := newTestAgent(t, "auto", nil, script)
	if err := ag.RunTurn(context.Background(), "write hello"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hi\n" {
		t.Fatalf("file %q", body)
	}
	if len(script.Seen) != 2 {
		t.Fatalf("model calls: %d", len(script.Seen))
	}
	sawTool := false
	for _, m := range script.Seen[1] {
		if m.Role == "tool" && strings.Contains(m.Content, "wrote") {
			sawTool = true
		}
	}
	if !sawTool {
		t.Fatal("second call did not see the tool result")
	}
	loaded, err := ag.Session.Messages()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) < 4 {
		t.Fatalf("session messages: %d", len(loaded))
	}
}

func TestBashFenceFallback(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{Content: "working\n```bash\nprintf hi > hello.txt\n```\n"},
		{Content: "done"},
	}}
	ag, dir := newTestAgent(t, "auto", nil, script)
	if err := ag.RunTurn(context.Background(), "create hello"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hi" {
		t.Fatalf("file %q", body)
	}
}

func TestNativeToolCallSkipsFence(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{
			Content: "```bash\nprintf no > no.txt\n```",
			ToolCalls: []provider.ToolCall{{
				ID: "c1", Name: "write_file", Arguments: `{"path":"yes.txt","content":"y"}`,
			}},
		},
		{Content: "done"},
	}}
	ag, dir := newTestAgent(t, "auto", nil, script)
	if err := ag.RunTurn(context.Background(), "write"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "no.txt")); !os.IsNotExist(err) {
		t.Fatal("fenced bash ran alongside a native tool call")
	}
	if _, err := os.Stat(filepath.Join(dir, "yes.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestMaxSteps(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "a", Name: "list_dir", Arguments: `{"path":"."}`}}},
		{ToolCalls: []provider.ToolCall{{ID: "b", Name: "list_dir", Arguments: `{"path":"."}`}}},
		{Content: "should not be requested"},
	}}
	ag, _ := newTestAgent(t, "auto", nil, script)
	ag.MaxSteps = 2
	err := ag.RunTurn(context.Background(), "loop")
	if err == nil || !strings.Contains(err.Error(), "max steps") {
		t.Fatalf("got %v", err)
	}
	if len(script.Seen) != 2 {
		t.Fatalf("calls: %d", len(script.Seen))
	}
}

func TestPlanModeAndDenyRule(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "write_file", Arguments: `{"path":"nope.txt","content":"x"}`,
		}}},
		{Content: "I could not write it"},
	}}
	ag, dir := newTestAgent(t, "plan", nil, script)
	if err := ag.RunTurn(context.Background(), "write a file"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.txt")); !os.IsNotExist(err) {
		t.Fatal("plan mode wrote a file")
	}
	denied := false
	for _, m := range script.Seen[1] {
		if m.Role == "tool" && strings.Contains(m.Content, "denied") {
			denied = true
		}
	}
	if !denied {
		t.Fatal("model did not see the denial")
	}

	script = &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "bash", Arguments: `{"command":"touch nope.txt"}`,
		}}},
		{Content: "stopped"},
	}}
	ag, dir = newTestAgent(t, "auto", []approval.Rule{{
		Action: approval.Deny, Pattern: "bash:touch *",
	}}, script)
	if err := ag.RunTurn(context.Background(), "touch a file"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.txt")); !os.IsNotExist(err) {
		t.Fatal("deny rule did not block bash")
	}
}

func TestFencesIgnoreOtherLanguages(t *testing.T) {
	calls := bashFences("```go\nfmt.Println(1)\n```\n```bash\necho a\n```\n```sh\necho b\n```")
	if len(calls) != 2 {
		t.Fatalf("calls: %+v", calls)
	}
	if calls[0].Name != "bash" || !strings.Contains(calls[0].Arguments, "echo a") {
		t.Fatalf("first: %+v", calls[0])
	}
}

func TestArgumentPreview(t *testing.T) {
	got := argumentPreview(provider.ToolCall{Name: "bash", Arguments: `{"command":"go test ./..."}`})
	if got != "go test ./..." {
		t.Fatal(got)
	}
	got = argumentPreview(provider.ToolCall{Name: "edit_file", Arguments: `{"path":"main.go","old_string":"x"}`})
	if got != "main.go" {
		t.Fatal(got)
	}
}
