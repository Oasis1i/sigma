// Package provider talks to an OpenAI-compatible chat completions endpoint.
//
// The HTTP client is hand-written. The wire format is small, and reading it
// is more useful in a learning project than wrapping the generated SDK. The
// Client interface is what the agent depends on, so tests use Scripted.
package provider

import "context"

// Message is one turn in the conversation. Tool results use Role "tool".
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is one function call requested by the model, or synthesized from
// a fenced bash block.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolSpec is the JSON schema sent to the model as a function tool.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Usage is the token accounting from one completion, when the server sends it.
// Zero means the server did not report usage. Sigma does not estimate here;
// a missing count stays missing so the status bar can say so.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Response is the completed assistant turn. Content is the full text even
// when it was also delivered through onDelta.
type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

// Client completes one chat turn. onDelta is called for each piece of
// assistant text as it arrives. onDelta may be nil. Tool-call bytes are not
// passed to onDelta.
type Client interface {
	Complete(ctx context.Context, messages []Message, tools []ToolSpec, onDelta func(string)) (Response, error)
}

// ModelSwitch is implemented by clients that can change model mid-session.
// The engine calls it from /model when no turn is running.
type ModelSwitch interface {
	SetModel(name string)
	CurrentModel() string
}
