package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/session"
)

// compactKeep is how many recent non-system messages survive a summary.
// It is large enough to hold a user request, the assistant's tool call, and
// the tool result, and small enough that the summary does the real work.
const compactKeep = 6

// EstimateTokens is a rough stand-in for a tokenizer: about four characters
// per token, counting message text and tool-call JSON. It is intentionally
// simple. A real tokenizer would be more accurate and would be a dependency.
func EstimateTokens(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Role) + len(m.Content) + len(m.Name) + len(m.ToolCallID)
		for _, c := range m.ToolCalls {
			n += len(c.Name) + len(c.Arguments) + len(c.ID)
		}
	}
	if n == 0 {
		return 0
	}
	return n/4 + 1
}

// compact summarizes older turns when the estimate exceeds CompactThreshold.
// A failure to summarize does not fail the user's turn: the full history is
// still valid, it is just long. A canceled context does fail the turn.
func (a *Agent) compact(ctx context.Context) error {
	if a == nil || a.CompactThreshold <= 0 || a.Client == nil {
		return nil
	}
	estimated := EstimateTokens(a.Messages)
	if estimated <= a.CompactThreshold {
		return nil
	}
	system, head, tail := splitHistory(a.Messages, compactKeep)
	if len(head) == 0 {
		return nil
	}
	summary, usage, err := a.summarize(ctx, head)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.notice("compaction skipped: " + err.Error())
		return nil
	}
	a.Hooks.usage(usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	record := session.Compaction{
		Summary:   summary,
		Keep:      len(tail),
		Dropped:   len(head),
		Estimated: estimated,
	}
	if a.Session != nil {
		if err := a.Session.AppendCompaction(record); err != nil {
			return err
		}
	}
	rebuilt := append([]provider.Message{}, system...)
	rebuilt = append(rebuilt, head...)
	rebuilt = append(rebuilt, tail...)
	a.Messages = session.ApplyCompaction(rebuilt, record)
	a.notice(fmt.Sprintf("compacted %d earlier messages (~%d tokens)", len(head), estimated))
	return nil
}

func (a *Agent) summarize(ctx context.Context, head []provider.Message) (string, provider.Usage, error) {
	prompt := "Summarize this coding-agent transcript for the model that will continue the task. " +
		"Include the user's goal, files read or changed, commands and their results, and errors worth retrying. " +
		"Keep paths and important error text. Do not call tools.\n\n" + transcriptText(head)
	resp, err := a.Client.Complete(ctx, []provider.Message{
		{Role: "system", Content: "You write compact, factual summaries of coding sessions."},
		{Role: "user", Content: prompt},
	}, nil, nil)
	if err != nil {
		return "", provider.Usage{}, err
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return "", resp.Usage, fmt.Errorf("model returned an empty summary")
	}
	return summary, resp.Usage, nil
}

// splitHistory divides messages into the system prompt, the middle that will
// be summarized, and the recent tail that stays verbatim. The tail never
// starts on a tool result: that result would be separated from the assistant
// message that requested it, and the API rejects an orphaned tool message.
func splitHistory(msgs []provider.Message, keep int) (system, head, tail []provider.Message) {
	if keep < 2 {
		keep = compactKeep
	}
	rest := msgs
	if len(msgs) > 0 && msgs[0].Role == "system" {
		system = msgs[:1]
		rest = msgs[1:]
	}
	if len(rest) <= keep {
		return system, nil, rest
	}
	cut := len(rest) - keep
	for cut > 0 && rest[cut].Role == "tool" {
		cut--
	}
	if cut <= 0 {
		return system, nil, rest
	}
	return system, rest[:cut], rest[cut:]
}

func transcriptText(msgs []provider.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s]\n", m.Role)
		if m.Content != "" {
			b.WriteString(m.Content)
			b.WriteByte('\n')
		}
		for _, c := range m.ToolCalls {
			fmt.Fprintf(&b, "tool %s %s\n", c.Name, c.Arguments)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
