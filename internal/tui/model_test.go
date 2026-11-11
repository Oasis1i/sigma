package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Oasis1i/sigma/internal/engine"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/tools"
)

type fakeCtl struct {
	cmds     []engine.Command
	slashes  []string
	mode     string
	model    string
	session  string
	ws       string
	messages []provider.Message
	prompt   int
	compl    int
}

func (f *fakeCtl) Submit(c engine.Command) { f.cmds = append(f.cmds, c) }

func (f *fakeCtl) ApplySlash(line string) (engine.SlashEffect, error) {
	f.slashes = append(f.slashes, line)
	fields := strings.Fields(line)
	switch fields[0] {
	case "/quit", "/exit":
		return engine.SlashEffect{Quit: true}, nil
	case "/mode":
		if len(fields) != 2 {
			return engine.SlashEffect{}, errString("usage: /mode plan|ask|auto")
		}
		f.mode = fields[1]
		return engine.SlashEffect{Notice: "approval mode: " + f.mode, Mode: f.mode}, nil
	case "/resume":
		return engine.SlashEffect{Notice: "resumed " + f.session, Rebuild: true}, nil
	default:
		return engine.SlashEffect{}, errString("unknown command " + fields[0])
	}
}

func (f *fakeCtl) Mode() string                 { return f.mode }
func (f *fakeCtl) ModelName() string            { return f.model }
func (f *fakeCtl) SessionID() string            { return f.session }
func (f *fakeCtl) Workspace() string            { return f.ws }
func (f *fakeCtl) Messages() []provider.Message { return f.messages }
func (f *fakeCtl) Usage() (int, int)            { return f.prompt, f.compl }

type errString string

func (e errString) Error() string { return string(e) }

func newTestModel(f *fakeCtl) *Model {
	if f.mode == "" {
		f.mode = "ask"
	}
	if f.model == "" {
		f.model = "fake-model"
	}
	if f.session == "" {
		f.session = "sess-1"
	}
	if f.ws == "" {
		f.ws = "/tmp/workspace"
	}
	return New(f, nil, "0.2.0")
}

func press(m *Model, key tea.KeyMsg) tea.Cmd {
	next, cmd := m.Update(key)
	if next != m {
		panic("Update returned a different model pointer")
	}
	return cmd
}

func enter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestSubmitMessage(t *testing.T) {
	f := &fakeCtl{}
	m := newTestModel(f)
	m.ta.SetValue("fix the test")
	press(m, enter())
	if len(f.cmds) != 1 || f.cmds[0].Kind != engine.CmdMessage || f.cmds[0].Text != "fix the test" {
		t.Fatalf("cmds %+v", f.cmds)
	}
	if !m.running {
		t.Fatal("expected running")
	}
	if m.blocks[len(m.blocks)-1].kind != blockUser || m.blocks[len(m.blocks)-1].text != "fix the test" {
		t.Fatalf("last block %+v", m.blocks[len(m.blocks)-1])
	}
	if strings.TrimSpace(m.ta.Value()) != "" {
		t.Fatalf("input not cleared: %q", m.ta.Value())
	}
}

func TestSlashQuitAndMode(t *testing.T) {
	f := &fakeCtl{}
	m := newTestModel(f)
	m.ta.SetValue("/mode plan")
	if cmd := press(m, enter()); cmd != nil {
		t.Fatal("mode change should not quit")
	}
	if m.mode != "plan" {
		t.Fatalf("mode %s", m.mode)
	}
	m.ta.SetValue("/quit")
	cmd := press(m, enter())
	if cmd == nil {
		t.Fatal("quit did not return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("cmd produced %T", cmd())
	}
}

func TestApprovalKeys(t *testing.T) {
	f := &fakeCtl{}
	m := newTestModel(f)
	m.Update(engine.Event{
		Kind: engine.KindApproval, ApprovalID: "apr_1", Tool: "bash", Args: "go test ./...",
	})
	if m.pending == nil {
		t.Fatal("pending not set")
	}
	press(m, runeKey('y'))
	if m.pending != nil {
		t.Fatal("approval still pending")
	}
	if len(f.cmds) != 1 || !f.cmds[0].Allow || f.cmds[0].Always || f.cmds[0].ApprovalID != "apr_1" {
		t.Fatalf("cmds %+v", f.cmds)
	}

	m.Update(engine.Event{Kind: engine.KindApproval, ApprovalID: "apr_2", Tool: "bash", Args: "git push"})
	press(m, runeKey('a'))
	if !f.cmds[1].Allow || !f.cmds[1].Always {
		t.Fatalf("always %+v", f.cmds[1])
	}
	m.Update(engine.Event{Kind: engine.KindApproval, ApprovalID: "apr_3", Tool: "bash", Args: "rm"})
	press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if f.cmds[2].Allow {
		t.Fatal("esc should deny")
	}
}

func TestCtrlCInterruptsThenQuits(t *testing.T) {
	f := &fakeCtl{}
	m := newTestModel(f)
	m.ta.SetValue("run")
	press(m, enter())

	// The first ctrl+c cancels the turn. A second one inside two seconds quits,
	// which matches the plain REPL's "ctrl-c again to exit".
	if cmd := press(m, tea.KeyMsg{Type: tea.KeyCtrlC}); cmd != nil {
		t.Fatal("first ctrl+c should not quit")
	}
	if len(f.cmds) < 2 || f.cmds[len(f.cmds)-1].Kind != engine.CmdCancel {
		t.Fatalf("cmds %+v", f.cmds)
	}
	cmd := press(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("second ctrl+c should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("%T", cmd())
	}
}

func TestTranscriptEventsAndFold(t *testing.T) {
	f := &fakeCtl{}
	m := newTestModel(f)
	m.Update(engine.Event{Kind: engine.KindStep, Step: 1, MaxSteps: 50})
	m.Update(engine.Event{Kind: engine.KindTextDelta, Text: "Hello"})
	m.Update(engine.Event{Kind: engine.KindTextDelta, Text: " **there**"})
	m.Update(engine.Event{
		Kind: engine.KindToolStart, CallID: "c1", Tool: "edit_file",
		Preview: "add.go",
		Diff:    "--- a/add.go\n+++ b/add.go\n@@\n-return a - b\n+return a + b\n",
	})
	m.Update(engine.Event{
		Kind: engine.KindToolEnd, CallID: "c1", Tool: "edit_file",
		Output: "edited add.go", Diff: "--- a/add.go\n+++ b/add.go\n@@\n-return a - b\n+return a + b\n",
	})
	m.Update(engine.Event{Kind: engine.KindUsage, PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14})
	m.Update(engine.Event{Kind: engine.KindDone})

	var assistant, tool *block
	for i := range m.blocks {
		switch m.blocks[i].kind {
		case blockAssistant:
			assistant = &m.blocks[i]
		case blockTool:
			tool = &m.blocks[i]
		}
	}
	if assistant == nil || assistant.text != "Hello **there**" || assistant.streaming {
		t.Fatalf("assistant %+v", assistant)
	}
	if tool == nil || tool.running || tool.collapsed || !strings.Contains(tool.diff, "+return a + b") {
		t.Fatalf("tool %+v", tool)
	}
	if m.running || m.step != 1 || m.maxStep != 50 {
		t.Fatalf("running %v step %d/%d", m.running, m.step, m.maxStep)
	}
	if m.promptTokens != 10 || m.completionTokens != 4 {
		t.Fatalf("tokens %d %d", m.promptTokens, m.completionTokens)
	}
	press(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !tool.collapsed {
		t.Fatal("ctrl+o should fold the tool")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "fake-model") || !strings.Contains(view, "ask") || !strings.Contains(view, "tok 14") {
		t.Fatalf("status bar missing fields:\n%s", view)
	}
	if !strings.Contains(view, "edit_file") {
		t.Fatalf("tool header missing:\n%s", view)
	}
}

func TestResumeRebuildsTranscript(t *testing.T) {
	f := &fakeCtl{messages: []provider.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "fix add"},
		{Role: "assistant", Content: "editing", ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "edit_file", Arguments: `{"path":"add.go","old_string":"a","new_string":"b"}`,
		}}},
		{Role: "tool", ToolCallID: "c1", Content: "edited add.go"},
	}}
	m := newTestModel(f)
	// New already rebuilt because the session has a conversation.
	foundTool := false
	for _, blk := range m.blocks {
		if blk.kind == blockTool && blk.output == "edited add.go" && blk.preview == "add.go" && strings.Contains(blk.diff, "-a") {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("blocks %+v", m.blocks)
	}
}

func TestModeNoteIsNotAUserBubble(t *testing.T) {
	f := &fakeCtl{messages: []provider.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "Approval mode is now auto."},
		{Role: "user", Content: "fix the failing test"},
	}}
	m := newTestModel(f)
	var sawNote, sawUser bool
	for _, blk := range m.blocks {
		if blk.kind == blockUser && strings.Contains(blk.text, "Approval mode") {
			t.Fatalf("mode note rendered as you: %+v", blk)
		}
		if blk.kind == blockNotice && blk.text == "Approval mode is now auto." {
			sawNote = true
		}
		if blk.kind == blockUser && blk.text == "fix the failing test" {
			sawUser = true
		}
	}
	if !sawNote || !sawUser {
		t.Fatalf("blocks %+v", m.blocks)
	}
}

func TestSubagentAndTodosRender(t *testing.T) {
	m := newTestModel(&fakeCtl{})
	m.Update(engine.Event{Kind: engine.KindSubagent, Phase: "start", SubagentID: "sub_1", Text: "where is add?"})
	m.Update(engine.Event{Kind: engine.KindSubagent, Phase: "step", SubagentID: "sub_1", Step: 1, MaxSteps: 4, Text: "list_dir ."})
	m.Update(engine.Event{Kind: engine.KindSubagent, Phase: "end", SubagentID: "sub_1", Text: "add is in add.go"})
	m.Update(engine.Event{Kind: engine.KindTodo, Todos: []tools.Todo{{ID: "1", Content: "fix add", Status: "pending"}}})
	var sub *block
	for i := range m.blocks {
		if m.blocks[i].kind == blockSubagent {
			sub = &m.blocks[i]
		}
	}
	if sub == nil || !sub.collapsed || sub.output != "add is in add.go" || len(sub.steps) == 0 {
		t.Fatalf("subagent %+v", sub)
	}
	if len(m.todos) != 1 || m.todos[0].Content != "fix add" {
		t.Fatalf("todos %+v", m.todos)
	}
	if !strings.Contains(stripANSI(m.View()), "fix add") {
		t.Fatal("todo panel missing")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		j := i + 1
		for j < len(s) && s[j] != 'm' {
			j++
		}
		if j < len(s) {
			i = j
		}
	}
	return b.String()
}
