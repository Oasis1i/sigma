// Package agent is the loop: send the conversation to the model, run any
// tool calls the policy allows, append the results, and repeat.
//
// A turn ends when the model replies with no tool calls, or when MaxSteps
// model calls have been made. Tool errors are sent back to the model instead
// of aborting the turn, so it can correct a bad edit or a failing test.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/session"
	"github.com/Oasis1i/sigma/internal/tools"
)

// AskFunc prompts the user for a call the policy classified as ask.
// allow is whether this call may run. always remembers the tool name for
// the rest of the session; it does not override deny rules.
type AskFunc func(tool, arg string) (allow bool, always bool, err error)

// Agent runs turns against one workspace.
type Agent struct {
	Client   provider.Client
	Registry *tools.Registry
	Policy   *approval.Policy
	Session  *session.Session
	Messages []provider.Message
	MaxSteps int
	Ask      AskFunc
	// Out receives assistant text. Err receives steps, tool traces, and denials.
	Out io.Writer
	Err io.Writer
}

// RunTurn appends one user message and loops until the model stops calling
// tools or the step limit is hit.
func (a *Agent) RunTurn(ctx context.Context, userText string) error {
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return fmt.Errorf("empty task")
	}
	if err := a.append(provider.Message{Role: "user", Content: userText}); err != nil {
		return err
	}
	maxSteps := a.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 50
	}
	for step := 1; step <= maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.status("· step %d/%d\n", step, maxSteps)
		resp, err := a.Client.Complete(ctx, a.Messages, a.toolSpecs(), func(delta string) {
			if a.Out != nil && delta != "" {
				fmt.Fprint(a.Out, delta)
			}
		})
		if err != nil {
			return err
		}
		if a.Out != nil && resp.Content != "" && !strings.HasSuffix(resp.Content, "\n") {
			fmt.Fprintln(a.Out)
		}

		calls := resp.ToolCalls
		if len(calls) == 0 {
			calls = bashFences(resp.Content)
			if len(calls) > 0 {
				a.status("· no native tool call; using a fenced bash block\n")
			}
		}
		for i := range calls {
			if calls[i].ID == "" {
				calls[i].ID = fmt.Sprintf("call_%d_%d", step, i+1)
			}
			if strings.TrimSpace(calls[i].Arguments) == "" {
				calls[i].Arguments = "{}"
			}
		}
		if err := a.append(provider.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: calls,
		}); err != nil {
			return err
		}
		if len(calls) == 0 {
			return nil
		}
		for i, call := range calls {
			output, stop := a.execCall(ctx, call)
			if err := a.append(provider.Message{
				Role:       "tool",
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    output,
			}); err != nil {
				return err
			}
			if stop {
				// The transcript must stay valid: every tool call needs a result.
				for _, rest := range calls[i+1:] {
					if err := a.append(provider.Message{
						Role:       "tool",
						Name:       rest.Name,
						ToolCallID: rest.ID,
						Content:    "Error: interrupted",
					}); err != nil {
						return err
					}
				}
				return ctx.Err()
			}
		}
	}
	return fmt.Errorf("reached max steps (%d) without a final answer", maxSteps)
}

// Note appends a user message that is not a new task, such as a mode change.
func (a *Agent) Note(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return a.append(provider.Message{Role: "user", Content: text})
}

func (a *Agent) execCall(ctx context.Context, call provider.ToolCall) (output string, stop bool) {
	if err := ctx.Err(); err != nil {
		return "Error: interrupted", true
	}
	spec, known := a.Registry.Get(call.Name)
	arg := argumentPreview(call)
	var dec approval.Decision
	if !known {
		dec = approval.Decision{Action: approval.Deny, Reason: "unknown tool"}
	} else {
		dec = a.Policy.Decide(call.Name, arg, spec.ReadOnly)
	}
	remember := false
	if dec.Action == approval.Ask {
		if a.Ask == nil {
			dec.Action = approval.Deny
			dec.Reason = "approval required but input is not a terminal"
		} else {
			allow, always, err := a.Ask(call.Name, arg)
			if err != nil {
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return "Error: interrupted", true
				}
				return "Error: " + err.Error(), false
			}
			if allow {
				dec.Action = approval.Allow
				dec.Reason = "user approved"
				if always {
					dec.Reason = "user approved (always)"
					remember = true
					a.Policy.RememberAlways(call.Name)
				}
			} else {
				dec.Action = approval.Deny
				dec.Reason = "user denied"
			}
		}
	}
	a.logApproval(call.Name, arg, dec, remember)
	if dec.Action != approval.Allow {
		if dec.Rule != "" {
			a.status("· denied %s (%s: %s)\n", call.Name, dec.Reason, dec.Rule)
			return fmt.Sprintf("Error: denied by rule %q (%s)", dec.Rule, dec.Reason), false
		}
		a.status("· denied %s (%s)\n", call.Name, dec.Reason)
		return fmt.Sprintf("Error: denied (%s)", dec.Reason), false
	}

	a.status("· %s %s\n", call.Name, oneLine(arg, 120))
	out, err := a.Registry.Execute(ctx, call.Name, json.RawMessage(call.Arguments))
	if ctx.Err() != nil {
		return "Error: interrupted", true
	}
	if err != nil {
		a.status("· %s error: %s\n", call.Name, oneLine(err.Error(), 200))
		return "Error: " + err.Error(), false
	}
	return out, false
}

func (a *Agent) logApproval(tool, arg string, dec approval.Decision, remember bool) {
	if a.Session == nil {
		return
	}
	if len(arg) > 4000 {
		arg = arg[:4000] + "..."
	}
	_ = a.Session.AppendApproval(session.Approval{
		Tool:     tool,
		Arg:      arg,
		Action:   string(dec.Action),
		Rule:     dec.Rule,
		Reason:   dec.Reason,
		Remember: remember,
	})
}

func (a *Agent) append(m provider.Message) error {
	a.Messages = append(a.Messages, m)
	if a.Session == nil {
		return nil
	}
	return a.Session.AppendMessage(m)
}

func (a *Agent) toolSpecs() []provider.ToolSpec {
	if a.Registry == nil {
		return nil
	}
	specs := a.Registry.Specs()
	out := make([]provider.ToolSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, provider.ToolSpec{
			Name:        s.Name,
			Description: s.Description,
			Parameters:  s.Parameters,
		})
	}
	return out
}

func (a *Agent) status(format string, args ...any) {
	if a.Err == nil {
		return
	}
	fmt.Fprintf(a.Err, format, args...)
}

// argumentPreview is the string rules match against: the bash command, or the path.
func argumentPreview(call provider.ToolCall) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &m); err != nil {
		return call.Arguments
	}
	if call.Name == "bash" {
		if s, ok := m["command"].(string); ok {
			return s
		}
	}
	if s, ok := m["path"].(string); ok && s != "" {
		return s
	}
	if call.Name == "grep" {
		return "."
	}
	if s, ok := m["command"].(string); ok {
		return s
	}
	return call.Arguments
}

func oneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if n > 0 && len(s) > n {
		return s[:n] + "..."
	}
	return s
}
