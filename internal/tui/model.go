// Package tui is the Bubble Tea interface.
//
// It is a client of the engine, the same way the plain REPL is. Nothing in
// this package calls the model or a tool. It sends commands and draws events.
// The Update method is covered by tests without a real terminal: they pass
// key messages and engine events in directly.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"github.com/Oasis1i/sigma/internal/agent"
	"github.com/Oasis1i/sigma/internal/engine"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/session"
)

// Controller is the slice of the engine the TUI uses. *engine.Engine
// implements it. Tests use a fake so key handling can be checked without
// starting the command loop.
type Controller interface {
	Submit(engine.Command)
	ApplySlash(line string) (engine.SlashEffect, error)
	Mode() string
	ModelName() string
	SessionID() string
	Workspace() string
	Messages() []provider.Message
	Usage() (prompt, completion int)
}

// Run starts the full-screen program. It returns when the user quits.
func Run(ctl Controller, events <-chan engine.Event, version string) error {
	m := New(ctl, events, version)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockTool
	blockNotice
	blockError
)

type block struct {
	kind      blockKind
	text      string
	rendered  string
	renderFor int

	callID    string
	tool      string
	args      string
	preview   string
	output    string
	diff      string
	failed    bool
	running   bool
	collapsed bool
	streaming bool
}

// Model is the Bubble Tea model. The viewport holds the transcript, the
// textarea holds the next message, and pending is set while the engine is
// waiting for y/n/always.
type Model struct {
	ctl     Controller
	events  <-chan engine.Event
	version string

	blocks []block
	follow bool
	vp     viewport.Model
	ta     textarea.Model

	width, height                      int
	mode, modelName, sessionID, wsPath string
	step, maxStep                      int
	promptTokens, completionTokens     int
	running                            bool
	pending                            *engine.Event
	lastInterrupt                      time.Time
	ready                              bool

	mdWidth int
	md      *glamour.TermRenderer
}

// New builds a model. If the session already has a conversation (a resume),
// the transcript is rebuilt from it. Otherwise a short welcome is shown.
func New(ctl Controller, events <-chan engine.Event, version string) *Model {
	ta := textarea.New()
	ta.Placeholder = "message  ·  enter sends  ·  alt+enter newline"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxWidth = 2000
	ta.Prompt = "> "
	ta.SetHeight(3)
	ta.SetWidth(80)
	// Enter submits. A newline is an explicit chord so a multi-line task is
	// possible without sending on the first return.
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")
	ta.Focus()

	vp := viewport.New(80, 16)
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 3

	m := &Model{
		ctl:       ctl,
		events:    events,
		version:   version,
		follow:    true,
		vp:        vp,
		ta:        ta,
		width:     80,
		height:    24,
		mode:      ctl.Mode(),
		modelName: ctl.ModelName(),
		sessionID: ctl.SessionID(),
		wsPath:    ctl.Workspace(),
	}
	p, c := ctl.Usage()
	m.promptTokens, m.completionTokens = p, c
	if hasConversation(ctl.Messages()) {
		m.rebuild()
	} else {
		m.blocks = []block{{kind: blockNotice, text: m.welcome()}}
	}
	m.layout()
	return m
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.ta.Focus(), waitEvent(m.events))
}

func (m *Model) welcome() string {
	return fmt.Sprintf("Sigma %s\nmodel %s · mode %s\nworkspace %s\nsession %s\n/help · enter sends · alt+enter newline · ctrl+c interrupts · ctrl+o folds the latest tool",
		m.version, m.modelName, m.mode, m.wsPath, m.sessionID)
}

func hasConversation(msgs []provider.Message) bool {
	for _, msg := range msgs {
		if msg.Role != "system" {
			return true
		}
	}
	return false
}

func waitEvent(ch <-chan engine.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return ev
	}
}

func (m *Model) rebuild() {
	msgs := m.ctl.Messages()
	blocks := []block{{kind: blockNotice, text: "session " + m.ctl.SessionID()}}
	index := map[string]int{}
	for _, msg := range msgs {
		switch msg.Role {
		case "system":
			continue
		case "user":
			if session.IsModeNote(msg.Content) {
				blocks = append(blocks, block{kind: blockNotice, text: msg.Content})
				continue
			}
			blocks = append(blocks, block{kind: blockUser, text: msg.Content})
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				blocks = append(blocks, block{kind: blockAssistant, text: msg.Content})
			}
			for _, call := range msg.ToolCalls {
				blocks = append(blocks, block{
					kind:      blockTool,
					callID:    call.ID,
					tool:      call.Name,
					args:      call.Arguments,
					preview:   agent.ArgumentPreview(call.Name, call.Arguments),
					diff:      agent.ToolDiff(call.Name, call.Arguments),
					collapsed: true,
				})
				index[call.ID] = len(blocks) - 1
			}
		case "tool":
			i, ok := index[msg.ToolCallID]
			if !ok {
				continue
			}
			blocks[i].output = msg.Content
			blocks[i].failed = strings.HasPrefix(msg.Content, "Error:")
			blocks[i].running = false
		}
	}
	m.blocks = blocks
	m.sessionID = m.ctl.SessionID()
	m.mode = m.ctl.Mode()
}
