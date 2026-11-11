package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Oasis1i/sigma/internal/agent"
	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/checkpoint"
	"github.com/Oasis1i/sigma/internal/prompt"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/session"
	"github.com/Oasis1i/sigma/internal/tools"
)

// Options is the identity of a run. The engine needs it to open another
// session for /clear and /resume without asking the UI to rebuild the agent.
type Options struct {
	SessionDir string
	Model      string
	Models     []string
	Provider   string
	BaseURL    string
	// Replace builds a client when /provider switches vendor. Nil means the
	// session can change model but not provider.
	Replace   func(providerName, model string) (provider.Client, error)
	Workspace string
	// Verify is copied into a fresh system prompt on /clear.
	Verify []string
	// Checkpoints is optional. Nil disables snapshots, including outside a
	// git repository where the store reports itself unavailable.
	Checkpoints *checkpoint.Store
	// MCPStatus is the text for /mcp. Nil means no servers were configured.
	MCPStatus func() string
}

// Engine owns one agent and the goroutines around it.
//
// Two goroutines matter. The loop goroutine reads commands. A turn goroutine
// runs only while the model is working, and it is the only goroutine that
// calls RunTurn. Approval is the bridge: the turn blocks in ask, the loop
// delivers the UI's decision on a separate channel. If approval were just
// another read of the command channel from inside the turn, the loop would
// be stuck inside the turn and could not observe cancel.
type Engine struct {
	ag        *agent.Agent
	dir       string
	model     string
	models    []string
	provider  string
	replace   func(providerName, model string) (provider.Client, error)
	baseURL   string
	workspace string
	verify    []string
	cp        *checkpoint.Store
	mcpStatus func() string
	// execMode is the approval mode to restore after the user approves a plan.
	// Entering plan mode saves the mode that was active. Starting already in
	// plan mode leaves this as ask.
	execMode string

	events   chan Event
	cmds     chan Command
	decision chan decision

	root       context.Context
	rootCancel context.CancelFunc

	mu         sync.Mutex
	busy       bool
	turnCancel context.CancelFunc
	waiting    string

	apr atomic.Uint64

	promptTokens     int
	completionTokens int
}

type decision struct {
	allow  bool
	always bool
}

// New starts the command loop. It sets the agent's Ask and Hooks; a UI
// should not also set those. Close stops the loop and closes the session.
func New(ag *agent.Agent, opt Options) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		ag:         ag,
		dir:        opt.SessionDir,
		model:      opt.Model,
		models:     append([]string(nil), opt.Models...),
		provider:   opt.Provider,
		replace:    opt.Replace,
		baseURL:    opt.BaseURL,
		workspace:  opt.Workspace,
		verify:     append([]string(nil), opt.Verify...),
		cp:         opt.Checkpoints,
		mcpStatus:  opt.MCPStatus,
		execMode:   "ask",
		events:     make(chan Event, 128),
		cmds:       make(chan Command, 32),
		decision:   make(chan decision, 1),
		root:       ctx,
		rootCancel: cancel,
	}
	ag.Hooks = &agent.Hooks{
		TextDelta: func(text string) {
			e.emit(Event{Kind: KindTextDelta, Text: text})
		},
		Step: func(step, max int) {
			e.emit(Event{Kind: KindStep, Step: step, MaxSteps: max})
		},
		ToolStart: func(ev agent.ToolEvent) { e.emit(e.toolEvent(KindToolStart, ev)) },
		ToolEnd:   func(ev agent.ToolEvent) { e.emit(e.toolEvent(KindToolEnd, ev)) },
		Notice: func(text string) {
			e.emit(Event{Kind: KindNotice, Text: text})
		},
		Usage: func(prompt, completion, total int) {
			e.mu.Lock()
			e.promptTokens += prompt
			e.completionTokens += completion
			e.mu.Unlock()
			e.emit(Event{
				Kind:             KindUsage,
				PromptTokens:     prompt,
				CompletionTokens: completion,
				TotalTokens:      total,
			})
		},
		Subagent: func(ev agent.SubagentEvent) {
			e.emit(Event{
				Kind:       KindSubagent,
				Phase:      ev.Phase,
				SubagentID: ev.ID,
				Text:       ev.Text,
				Step:       ev.Step,
				MaxSteps:   ev.Max,
				Failed:     ev.Failed,
			})
		},
		Todo: func(items []tools.Todo) {
			e.emit(Event{Kind: KindTodo, Todos: items})
		},
	}
	if ag.Todos != nil {
		ag.Todos.SetOnChange(func(items []tools.Todo) {
			e.emit(Event{Kind: KindTodo, Todos: items})
		})
	}
	ag.Ask = e.ask
	go e.loop()
	return e
}

func (e *Engine) toolEvent(kind Kind, ev agent.ToolEvent) Event {
	return Event{
		Kind:    kind,
		CallID:  ev.ID,
		Tool:    ev.Name,
		Args:    ev.Args,
		Preview: ev.Preview,
		Output:  ev.Output,
		Diff:    ev.Diff,
		Failed:  ev.Failed,
	}
}

// Events is the stream. It is not closed. Read until KindDone for one turn,
// and stop when Close is called; further emits are dropped.
func (e *Engine) Events() <-chan Event { return e.events }

// Submit hands a command to the loop. It does not block the UI for long:
// the buffer covers a message plus the approval and cancel that can arrive
// while a turn is in flight. After Close, the command is discarded.
func (e *Engine) Submit(cmd Command) {
	select {
	case e.cmds <- cmd:
	case <-e.root.Done():
	}
}

// Cancel interrupts the running turn, if there is one.
func (e *Engine) Cancel() {
	e.Submit(Command{Kind: CmdCancel})
}

// Close stops the loop, cancels a running turn, and closes the session log.
func (e *Engine) Close() {
	e.rootCancel()
	if e.ag != nil && e.ag.Session != nil {
		_ = e.ag.Session.Close()
	}
}

// Busy reports whether a turn is running. Slash commands that swap the
// session refuse to run while this is true, because they replace Messages
// and the turn goroutine is appending to that slice.
func (e *Engine) Busy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.busy
}

// Mode is the current approval mode.
func (e *Engine) Mode() string {
	if e.ag == nil || e.ag.Policy == nil {
		return ""
	}
	return string(e.ag.Policy.Current())
}

// ModelName is the model from configuration, for the status bar.
func (e *Engine) ModelName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.model
}

// ProviderName is the current provider id, such as openai or chatgpt.
func (e *Engine) ProviderName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.provider == "" {
		return "openai"
	}
	return e.provider
}

// Use switches provider and model for the next turn. An empty provider keeps
// the current one. An empty model keeps the current one. Switching vendor
// needs Options.Replace.
func (e *Engine) Use(providerName, model string) error {
	if e.Busy() {
		return fmt.Errorf("wait for the current turn to finish before changing model")
	}
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if providerName == "" {
		providerName = e.ProviderName()
	}
	if model == "" {
		model = e.ModelName()
	}
	if providerName == e.ProviderName() {
		return e.SetModel(model)
	}
	if e.replace == nil {
		return fmt.Errorf("this session cannot switch providers")
	}
	client, err := e.replace(providerName, model)
	if err != nil {
		return err
	}
	if sw, ok := client.(provider.ModelSwitch); ok && model != "" {
		sw.SetModel(model)
	}
	if e.ag != nil {
		e.ag.Client = client
	}
	e.mu.Lock()
	e.provider = providerName
	e.model = model
	e.mu.Unlock()
	return nil
}

// Models is the configured list /model offers. Empty means any name is accepted.
func (e *Engine) Models() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.models))
	copy(out, e.models)
	return out
}

// SetModel changes the model used by the next turn. It refuses while a turn
// is running. When Models was configured, name must be in that list.
func (e *Engine) SetModel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("usage: /model <name>")
	}
	if e.Busy() {
		return fmt.Errorf("wait for the current turn to finish before changing model")
	}
	allowed := e.Models()
	if len(allowed) > 0 && !containsModel(allowed, name) {
		return fmt.Errorf("unknown model %q (configured: %s)", name, strings.Join(allowed, ", "))
	}
	if e.ag == nil || e.ag.Client == nil {
		return fmt.Errorf("no model client")
	}
	sw, ok := e.ag.Client.(provider.ModelSwitch)
	if !ok {
		return fmt.Errorf("this provider cannot switch models")
	}
	sw.SetModel(name)
	e.mu.Lock()
	e.model = name
	e.mu.Unlock()
	return nil
}

func containsModel(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// Workspace is the absolute workspace path.
func (e *Engine) Workspace() string { return e.workspace }

// SessionID is the open log's id, or empty when there is no session.
func (e *Engine) SessionID() string {
	if e.ag == nil || e.ag.Session == nil {
		return ""
	}
	return e.ag.Session.ID
}

// Usage is the sum of token counts reported since this process opened the
// session. It is not stored in the log. A server that never sends usage
// leaves both numbers at zero.
func (e *Engine) Usage() (prompt, completion int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.promptTokens, e.completionTokens
}

// Todos returns the current plan list. Nil when the agent has no list.
func (e *Engine) Todos() []tools.Todo {
	if e == nil || e.ag == nil || e.ag.Todos == nil {
		return nil
	}
	return e.ag.Todos.Snapshot()
}

// Messages returns a copy of the conversation. Call it when no turn is
// running (after /resume or /clear). The copy's backing array is new; the
// message contents are shared, and the loop does not mutate them in place.
func (e *Engine) Messages() []provider.Message {
	if e.ag == nil {
		return nil
	}
	out := make([]provider.Message, len(e.ag.Messages))
	copy(out, e.ag.Messages)
	return out
}

// SetMode changes the policy and rewrites the system prompt's "Approval mode:"
// line. It does not append a user message. Switching to plan remembers the
// previous mode so an approved plan can execute there.
func (e *Engine) SetMode(mode string) error {
	if e.Busy() {
		return fmt.Errorf("wait for the current turn to finish before changing mode")
	}
	return e.applyMode(mode)
}

// applyMode is SetMode without the busy check. The plan-approval path calls
// it from the turn goroutine, which is the only writer at that moment.
func (e *Engine) applyMode(mode string) error {
	if e.ag == nil || e.ag.Policy == nil {
		return fmt.Errorf("no approval policy")
	}
	if mode == string(approval.ModePlan) && e.ag.Policy.Current() != approval.ModePlan {
		e.execMode = string(e.ag.Policy.Current())
	}
	prev := e.Mode()
	if err := e.ag.Policy.SetMode(mode); err != nil {
		return err
	}
	if err := e.retargetSystemPrompt(); err != nil {
		_ = e.ag.Policy.SetMode(prev)
		return err
	}
	return nil
}

// retargetSystemPrompt replaces the leading system message and appends the
// same text to the log. Resume keeps the latest prompt and drops the earlier
// copy, so the model sees one system message with the current mode.
func (e *Engine) retargetSystemPrompt() error {
	sys, err := e.systemPrompt()
	if err != nil {
		return err
	}
	msg := provider.Message{Role: "system", Content: sys}
	if len(e.ag.Messages) > 0 && e.ag.Messages[0].Role == "system" {
		e.ag.Messages[0] = msg
	} else {
		e.ag.Messages = append([]provider.Message{msg}, e.ag.Messages...)
	}
	if e.ag.Session == nil {
		return nil
	}
	return e.ag.Session.AppendMessage(msg)
}

// ListSessions reads the session directory, newest first.
func (e *Engine) ListSessions() ([]session.Summary, error) {
	if e.dir == "" {
		return nil, fmt.Errorf("session directory is not set")
	}
	return session.List(e.dir)
}

// Clear starts a new session and drops the in-memory conversation back to
// the system prompt. The previous log stays on disk and can be resumed.
func (e *Engine) Clear() (string, error) {
	if e.Busy() {
		return "", fmt.Errorf("wait for the current turn to finish")
	}
	if e.dir == "" {
		return "", fmt.Errorf("session directory is not set")
	}
	sess, err := session.Create(e.dir, map[string]string{
		"model":     e.model,
		"base_url":  e.baseURL,
		"mode":      e.Mode(),
		"workspace": e.workspace,
	})
	if err != nil {
		return "", err
	}
	sys, err := e.systemPrompt()
	if err != nil {
		_ = sess.Close()
		return "", err
	}
	msg := provider.Message{Role: "system", Content: sys}
	if err := sess.AppendMessage(msg); err != nil {
		_ = sess.Close()
		return "", err
	}
	e.swapSession(sess, []provider.Message{msg})
	return sess.ID, nil
}

// Resume opens a previous log by id or unique prefix and rebuilds the
// conversation from it. The approval mode stays as the process started it;
// "always" grants are restored from the log.
func (e *Engine) Resume(id string) error {
	if e.Busy() {
		return fmt.Errorf("wait for the current turn to finish")
	}
	if e.dir == "" {
		return fmt.Errorf("session directory is not set")
	}
	sess, err := session.Open(e.dir, id)
	if err != nil {
		return err
	}
	if e.ag.Session != nil && sess.Path == e.ag.Session.Path {
		_ = sess.Close()
		return nil
	}
	messages, err := sess.Messages()
	if err != nil {
		_ = sess.Close()
		return err
	}
	remembered, err := sess.RememberedTools()
	if err != nil {
		_ = sess.Close()
		return err
	}
	if len(messages) == 0 {
		sys, err := e.systemPrompt()
		if err != nil {
			_ = sess.Close()
			return err
		}
		messages = []provider.Message{{Role: "system", Content: sys}}
		if err := sess.AppendMessage(messages[0]); err != nil {
			_ = sess.Close()
			return err
		}
	}
	e.swapSession(sess, messages)
	if e.ag.Policy != nil {
		e.ag.Policy.ResetSessionGrants()
		for _, tool := range remembered {
			e.ag.Policy.RememberAlways(tool)
		}
	}
	return nil
}

func (e *Engine) swapSession(sess *session.Session, messages []provider.Message) {
	if e.ag.Session != nil {
		_ = e.ag.Session.Close()
	}
	e.ag.Session = sess
	e.ag.Messages = messages
	if e.cp != nil {
		e.cp.SetSession(sess.ID)
	}
	e.mu.Lock()
	e.promptTokens = 0
	e.completionTokens = 0
	e.mu.Unlock()
}

func (e *Engine) systemPrompt() (string, error) {
	sections, err := prompt.LoadAgents(e.workspace)
	if err != nil {
		return "", err
	}
	return prompt.Build(e.workspace, e.Mode(), sections, e.verify), nil
}

func (e *Engine) loop() {
	for {
		select {
		case <-e.root.Done():
			return
		case cmd := <-e.cmds:
			e.handle(cmd)
		}
	}
}

func (e *Engine) handle(cmd Command) {
	switch cmd.Kind {
	case CmdMessage:
		e.startTurn(cmd.Text)
	case CmdCancel:
		e.mu.Lock()
		cancel := e.turnCancel
		e.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	case CmdApproval:
		e.deliver(cmd)
	default:
		e.emit(Event{Kind: KindError, Text: "unknown command"})
	}
}

func (e *Engine) startTurn(text string) {
	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		e.emit(Event{Kind: KindError, Text: "a turn is already running"})
		return
	}
	e.busy = true
	ctx, cancel := context.WithCancel(e.root)
	e.turnCancel = cancel
	e.mu.Unlock()

	go func() {
		e.snapshot(ctx)
		err := e.runPlanAware(ctx, text)
		e.mu.Lock()
		e.busy = false
		e.turnCancel = nil
		e.mu.Unlock()
		canceled := errors.Is(err, context.Canceled)
		if err != nil && !canceled {
			e.emit(Event{Kind: KindError, Text: err.Error()})
		}
		e.emit(Event{Kind: KindDone, Canceled: canceled})
	}()
}

// runPlanAware runs the user turn. If that turn was in plan mode and produced
// a todo list, it waits for approval and, on yes, continues in the saved
// execution mode. The whole sequence stays one turn so the UI can show the
// approval prompt before Done.
func (e *Engine) runPlanAware(ctx context.Context, text string) error {
	err := e.ag.RunTurn(ctx, text)
	if err != nil || e.ag.Policy == nil || e.ag.Policy.Current() != approval.ModePlan {
		return err
	}
	todos := []tools.Todo(nil)
	if e.ag.Todos != nil {
		todos = e.ag.Todos.Snapshot()
	}
	if len(todos) == 0 {
		return nil
	}
	allow, _, aerr := e.ask(ctx, "plan", formatTodos(todos))
	if aerr != nil {
		return aerr
	}
	if !allow {
		e.emit(Event{Kind: KindNotice, Text: "plan rejected"})
		return nil
	}
	execMode := e.execMode
	if execMode == "" || execMode == string(approval.ModePlan) {
		execMode = string(approval.ModeAsk)
	}
	if err := e.applyMode(execMode); err != nil {
		return err
	}
	e.emit(Event{Kind: KindNotice, Text: "plan approved; executing in " + execMode + " mode"})
	return e.ag.RunTurn(ctx, "The user approved the plan. Execute the todos in order. Update the todo list as you finish each item.")
}

func formatTodos(items []tools.Todo) string {
	var b strings.Builder
	for _, item := range items {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[%s] %s (%s)", item.ID, item.Content, item.Status)
	}
	return b.String()
}

// ask is the agent's AskFunc. It emits an approval event and waits for the
// matching command, or for the turn to be canceled. The decision channel is
// buffered so a UI that answers before this goroutine reaches select does
// not deadlock.
func (e *Engine) ask(ctx context.Context, tool, arg string) (bool, bool, error) {
	id := fmt.Sprintf("apr_%d", e.apr.Add(1))
	select {
	case <-e.decision:
	default:
	}
	e.mu.Lock()
	e.waiting = id
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if e.waiting == id {
			e.waiting = ""
		}
		e.mu.Unlock()
	}()

	e.emit(Event{Kind: KindApproval, ApprovalID: id, Tool: tool, Args: arg})
	select {
	case <-ctx.Done():
		return false, false, ctx.Err()
	case <-e.root.Done():
		return false, false, e.root.Err()
	case d := <-e.decision:
		return d.allow, d.always, nil
	}
}

func (e *Engine) deliver(cmd Command) {
	e.mu.Lock()
	waiting := e.waiting
	e.mu.Unlock()
	if waiting == "" {
		return
	}
	if cmd.ApprovalID != "" && cmd.ApprovalID != waiting {
		return
	}
	select {
	case e.decision <- decision{allow: cmd.Allow, always: cmd.Always}:
	default:
	}
}

func (e *Engine) snapshot(ctx context.Context) {
	if e.cp == nil || !e.cp.Enabled(ctx) {
		return
	}
	ref, err := e.cp.Snapshot(ctx)
	if err != nil {
		e.emit(Event{Kind: KindNotice, Text: "checkpoint skipped: " + err.Error()})
		return
	}
	e.emit(Event{Kind: KindNotice, Text: "checkpoint " + ref})
	if e.ag.Session != nil {
		_ = e.ag.Session.AppendCheckpoint(session.Checkpoint{Action: "snapshot", Ref: ref})
	}
}

func (e *Engine) undo() (SlashEffect, error) {
	if e.Busy() {
		return SlashEffect{}, fmt.Errorf("wait for the current turn to finish")
	}
	if e.cp == nil || !e.cp.Enabled(context.Background()) {
		return SlashEffect{Notice: "undo is unavailable outside a git repository"}, nil
	}
	ref, err := e.cp.Undo(context.Background())
	if err != nil {
		return SlashEffect{}, err
	}
	if e.ag.Session != nil {
		_ = e.ag.Session.AppendCheckpoint(session.Checkpoint{Action: "undo", Ref: ref})
	}
	return SlashEffect{Notice: "restored " + ref}, nil
}

func (e *Engine) emit(ev Event) {
	select {
	case <-e.root.Done():
	case e.events <- ev:
	}
}
