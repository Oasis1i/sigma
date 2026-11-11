// Package headless collects a one-shot turn into a JSON report.
//
// sigma -p --output json prints one object. --output jsonl prints each engine
// event as a line. Both are for scripts. The interactive UIs stay on text.
package headless

import (
	"encoding/json"
	"io"

	"github.com/Oasis1i/sigma/internal/engine"
)

// Report is the summary of one -p run. EditAttempts counts edit_file and
// write_file calls. EditFailures is how many of those returned an error.
// The rate is EditFailures/EditAttempts, which run.sh prints as a fraction.
type Report struct {
	Text             string         `json:"text"`
	Steps            int            `json:"steps"`
	MaxSteps         int            `json:"max_steps,omitempty"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	ToolCalls        int            `json:"tool_calls"`
	EditAttempts     int            `json:"edit_attempts"`
	EditFailures     int            `json:"edit_failures"`
	Error            string         `json:"error,omitempty"`
	Canceled         bool           `json:"canceled,omitempty"`
	Events           []engine.Event `json:"events,omitempty"`
}

// Add folds one event into the summary.
func (r *Report) Add(ev engine.Event) {
	switch ev.Kind {
	case engine.KindTextDelta:
		r.Text += ev.Text
	case engine.KindStep:
		r.Steps = ev.Step
		r.MaxSteps = ev.MaxSteps
	case engine.KindUsage:
		r.PromptTokens += ev.PromptTokens
		r.CompletionTokens += ev.CompletionTokens
	case engine.KindToolEnd:
		r.ToolCalls++
		if ev.Tool == "edit_file" || ev.Tool == "write_file" {
			r.EditAttempts++
			if ev.Failed {
				r.EditFailures++
			}
		}
	case engine.KindError:
		if r.Error == "" {
			r.Error = ev.Text
		}
	case engine.KindDone:
		r.Canceled = ev.Canceled
	}
}

// WriteJSON writes the report as one JSON object plus a newline.
func WriteJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// WriteJSONL writes one event as a single line.
func WriteJSONL(w io.Writer, ev engine.Event) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(ev)
}
