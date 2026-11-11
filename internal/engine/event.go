// Package engine is the UI-agnostic agent loop.
//
// The agent package still runs one turn: call the model, run tools, repeat.
// The engine wraps that turn in a command loop so a UI never has to share
// stdin with the model. UIs send commands (a user message, an approval
// decision, cancel) and read a stream of events (text, tools, approval
// requests, steps, usage, errors, done).
//
// The plain REPL, the TUI, and the web GUI all sit on this package. Approval
// is one of the commands. The engine does not read the terminal itself.
package engine

import "github.com/Oasis1i/sigma/internal/tools"

// Kind identifies one item on the event stream. The string values are stable:
// the web GUI serializes events as JSON and matches on them.
type Kind string

const (
	KindTextDelta Kind = "text_delta"
	KindToolStart Kind = "tool_start"
	KindToolEnd   Kind = "tool_end"
	KindApproval  Kind = "approval_request"
	KindStep      Kind = "step"
	KindUsage     Kind = "usage"
	KindNotice    Kind = "notice"
	KindError     Kind = "error"
	KindDone      Kind = "done"
	// KindSubagent is progress from a read-only explore/task child.
	// Phase is start, step, or end.
	KindSubagent Kind = "subagent"
	// KindTodo is the current plan list.
	KindTodo Kind = "todo"
)

// Event is one thing a UI should show or answer. Only the fields for Kind
// are set. Approval events wait until a CmdApproval command comes back;
// every other event is a notification.
type Event struct {
	Kind Kind `json:"kind"`

	// Text is assistant text, a notice, or an error message.
	Text string `json:"text,omitempty"`

	CallID  string `json:"call_id,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Args    string `json:"args,omitempty"`
	Preview string `json:"preview,omitempty"`
	Output  string `json:"output,omitempty"`
	Diff    string `json:"diff,omitempty"`
	Failed  bool   `json:"failed,omitempty"`

	// ApprovalID is set on KindApproval. The matching command must echo it.
	ApprovalID string `json:"approval_id,omitempty"`

	Step     int `json:"step,omitempty"`
	MaxSteps int `json:"max_steps,omitempty"`

	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`

	// Canceled is set on KindDone when the turn was interrupted.
	Canceled bool `json:"canceled,omitempty"`

	// Subagent fields. Phase is start, step, or end.
	Phase      string `json:"phase,omitempty"`
	SubagentID string `json:"subagent_id,omitempty"`

	Todos []tools.Todo `json:"todos,omitempty"`
}

// CmdKind is what a UI can ask the engine to do.
type CmdKind string

const (
	CmdMessage  CmdKind = "message"
	CmdApproval CmdKind = "approval"
	CmdCancel   CmdKind = "cancel"
)

// Command is one request from a UI. Approval commands are delivered to the
// turn that is blocked in Ask; they are not a new turn.
type Command struct {
	Kind       CmdKind `json:"kind"`
	Text       string  `json:"text,omitempty"`
	ApprovalID string  `json:"approval_id,omitempty"`
	Allow      bool    `json:"allow,omitempty"`
	Always     bool    `json:"always,omitempty"`
}
