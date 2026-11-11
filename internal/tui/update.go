package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Oasis1i/sigma/internal/engine"
	"github.com/Oasis1i/sigma/internal/tools"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		m.layout()
		return m, nil
	case engine.Event:
		m.apply(msg)
		return m, waitEvent(m.events)
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.follow = m.vp.AtBottom()
		return m, cmd
	case tea.KeyMsg:
		return m.onKey(msg)
	default:
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd
	}
}

func (m *Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Approval captures the next key. Ordinary typing must not land in the
	// textarea, or "y" would both approve and start a word.
	if m.pending != nil && !msg.Alt {
		return m.decideKey(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		return m.interrupt()
	case "ctrl+o":
		m.toggleLastTool()
		m.syncViewport()
		return m, nil
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.follow = m.vp.AtBottom()
		return m, cmd
	case "enter", "ctrl+m":
		return m.submit()
	case "alt+enter", "ctrl+j":
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd
	}
}

func (m *Model) decideKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m.interrupt()
	}
	switch strings.ToLower(msg.String()) {
	case "y":
		m.decide(true, false)
	case "a":
		m.decide(true, true)
	case "n", "esc":
		m.decide(false, false)
	}
	return m, nil
}

func (m *Model) decide(allow, always bool) {
	if m.pending == nil {
		return
	}
	id := m.pending.ApprovalID
	m.pending = nil
	m.ctl.Submit(engine.Command{
		Kind:       engine.CmdApproval,
		ApprovalID: id,
		Allow:      allow,
		Always:     always,
	})
	m.layout()
}

func (m *Model) interrupt() (tea.Model, tea.Cmd) {
	if !m.running && m.pending == nil {
		return m, tea.Quit
	}
	// A second ctrl+c while the turn is still stopping leaves the program.
	// The first one only cancels the turn, matching the plain REPL.
	if !m.lastInterrupt.IsZero() && time.Since(m.lastInterrupt) < 2*time.Second {
		m.ctl.Submit(engine.Command{Kind: engine.CmdCancel})
		return m, tea.Quit
	}
	m.lastInterrupt = time.Now()
	m.ctl.Submit(engine.Command{Kind: engine.CmdCancel})
	if m.pending != nil {
		// Cancel unblocks Ask via the turn context. Also send a deny so a
		// decision already in flight cannot approve the call after the
		// interrupt.
		m.decide(false, false)
	}
	return m, nil
}

func (m *Model) submit() (tea.Model, tea.Cmd) {
	if m.pending != nil {
		return m, nil
	}
	text := strings.TrimSpace(m.ta.Value())
	if text == "" {
		return m, nil
	}
	m.ta.Reset()
	if strings.HasPrefix(text, "/") && !strings.Contains(text, "\n") {
		return m.applySlash(text)
	}
	if m.running {
		m.blocks = append(m.blocks, block{kind: blockError, text: "a turn is already running (ctrl+c to interrupt)"})
		m.syncViewport()
		return m, nil
	}
	m.blocks = append(m.blocks, block{kind: blockUser, text: text})
	m.running = true
	m.step = 0
	m.lastInterrupt = time.Time{}
	m.ctl.Submit(engine.Command{Kind: engine.CmdMessage, Text: text})
	m.syncViewport()
	return m, nil
}

func (m *Model) applySlash(line string) (tea.Model, tea.Cmd) {
	effect, err := m.ctl.ApplySlash(line)
	if err != nil {
		m.blocks = append(m.blocks, block{kind: blockError, text: err.Error()})
		m.syncViewport()
		return m, nil
	}
	if effect.Notice != "" {
		m.blocks = append(m.blocks, block{kind: blockNotice, text: strings.TrimRight(effect.Notice, "\n")})
	}
	if effect.Rebuild {
		m.rebuild()
	}
	m.mode = m.ctl.Mode()
	m.sessionID = m.ctl.SessionID()
	m.promptTokens, m.completionTokens = m.ctl.Usage()
	m.syncViewport()
	if effect.Quit {
		m.ctl.Submit(engine.Command{Kind: engine.CmdCancel})
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) apply(ev engine.Event) {
	wasPending := m.pending != nil
	switch ev.Kind {
	case engine.KindTextDelta:
		m.appendDelta(ev.Text)
	case engine.KindToolStart:
		m.finishStream()
		m.collapseOlderTools()
		m.blocks = append(m.blocks, block{
			kind:    blockTool,
			callID:  ev.CallID,
			tool:    ev.Tool,
			args:    ev.Args,
			preview: ev.Preview,
			diff:    ev.Diff,
			running: true,
		})
	case engine.KindToolEnd:
		m.finishTool(ev)
	case engine.KindApproval:
		cp := ev
		m.pending = &cp
	case engine.KindStep:
		m.step = ev.Step
		m.maxStep = ev.MaxSteps
		m.running = true
	case engine.KindUsage:
		m.promptTokens += ev.PromptTokens
		m.completionTokens += ev.CompletionTokens
	case engine.KindNotice:
		m.finishStream()
		m.blocks = append(m.blocks, block{kind: blockNotice, text: ev.Text})
	case engine.KindSubagent:
		m.finishStream()
		m.applySubagent(ev)
	case engine.KindTodo:
		m.todos = append([]tools.Todo(nil), ev.Todos...)
		if m.ready {
			m.layout()
			return
		}
	case engine.KindError:
		m.finishStream()
		text := ev.Text
		if text == "" {
			text = "unknown error"
		}
		m.blocks = append(m.blocks, block{kind: blockError, text: text})
	case engine.KindDone:
		m.finishStream()
		m.running = false
		m.pending = nil
		if ev.Canceled {
			m.blocks = append(m.blocks, block{kind: blockNotice, text: "interrupted"})
		}
	}
	if m.ready && wasPending != (m.pending != nil) {
		m.layout()
		return
	}
	m.syncViewport()
}

func (m *Model) appendDelta(text string) {
	if text == "" {
		return
	}
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == blockAssistant && m.blocks[n-1].streaming {
		m.blocks[n-1].text += text
		m.blocks[n-1].rendered = ""
		return
	}
	m.blocks = append(m.blocks, block{kind: blockAssistant, text: text, streaming: true})
}

func (m *Model) finishStream() {
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == blockAssistant {
		m.blocks[n-1].streaming = false
		m.blocks[n-1].rendered = ""
	}
}

func (m *Model) finishTool(ev engine.Event) {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockTool && m.blocks[i].callID == ev.CallID {
			m.blocks[i].output = ev.Output
			m.blocks[i].failed = ev.Failed
			m.blocks[i].running = false
			if ev.Diff != "" {
				m.blocks[i].diff = ev.Diff
			}
			if ev.Preview != "" {
				m.blocks[i].preview = ev.Preview
			}
			return
		}
	}
	m.blocks = append(m.blocks, block{
		kind:    blockTool,
		callID:  ev.CallID,
		tool:    ev.Tool,
		args:    ev.Args,
		preview: ev.Preview,
		output:  ev.Output,
		diff:    ev.Diff,
		failed:  ev.Failed,
	})
}

func (m *Model) collapseOlderTools() {
	for i := range m.blocks {
		if m.blocks[i].kind == blockTool {
			m.blocks[i].collapsed = true
		}
	}
}

func (m *Model) applySubagent(ev engine.Event) {
	for i := range m.blocks {
		if m.blocks[i].kind == blockSubagent && m.blocks[i].callID == ev.SubagentID {
			switch ev.Phase {
			case "step":
				line := ev.Text
				if ev.Step > 0 {
					line = fmt.Sprintf("step %d/%d %s", ev.Step, ev.MaxSteps, ev.Text)
				}
				if strings.TrimSpace(line) != "" {
					m.blocks[i].steps = append(m.blocks[i].steps, strings.TrimSpace(line))
				}
			case "end":
				m.blocks[i].output = ev.Text
				m.blocks[i].failed = ev.Failed
				m.blocks[i].running = false
				m.blocks[i].collapsed = true
			}
			return
		}
	}
	m.blocks = append(m.blocks, block{
		kind:      blockSubagent,
		callID:    ev.SubagentID,
		text:      ev.Text,
		running:   ev.Phase != "end",
		collapsed: true,
	})
}

func (m *Model) toggleLastTool() {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == blockTool {
			m.blocks[i].collapsed = !m.blocks[i].collapsed
			return
		}
	}
}
