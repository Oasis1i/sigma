package agent

// ToolEvent describes one tool call for a UI. Args is the raw JSON the model
// sent. Preview is the short string the approval rules match (the command, or
// the path). Diff is set for edit_file and is the change the model asked for,
// whether or not the edit was applied.
type ToolEvent struct {
	ID      string
	Name    string
	Args    string
	Preview string
	Output  string
	Diff    string
	Failed  bool
}

// Hooks is how the loop tells a UI what happened, without knowing whether
// that UI is a terminal, a TUI, or a browser. A nil Hooks, and nil funcs
// inside it, are ignored, so tests that build an Agent directly stay quiet.
//
// The engine in internal/engine is the supported way to subscribe. It turns
// these callbacks into a single event stream and also carries approval, which
// is a question and not just a notification.
type Hooks struct {
	TextDelta func(text string)
	Step      func(step, max int)
	ToolStart func(ToolEvent)
	ToolEnd   func(ToolEvent)
	Notice    func(text string)
	Usage     func(prompt, completion, total int)
}

func (h *Hooks) textDelta(text string) {
	if h != nil && h.TextDelta != nil && text != "" {
		h.TextDelta(text)
	}
}

func (h *Hooks) step(n, max int) {
	if h != nil && h.Step != nil {
		h.Step(n, max)
	}
}

func (h *Hooks) toolStart(ev ToolEvent) {
	if h != nil && h.ToolStart != nil {
		h.ToolStart(ev)
	}
}

func (h *Hooks) toolEnd(ev ToolEvent) {
	if h != nil && h.ToolEnd != nil {
		h.ToolEnd(ev)
	}
}

func (h *Hooks) notice(text string) {
	if h != nil && h.Notice != nil && text != "" {
		h.Notice(text)
	}
}

func (h *Hooks) usage(prompt, completion, total int) {
	if h != nil && h.Usage != nil && (prompt > 0 || completion > 0 || total > 0) {
		h.Usage(prompt, completion, total)
	}
}
