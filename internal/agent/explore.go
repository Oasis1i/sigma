package agent

import (
	"context"
	"strings"

	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/tools"
)

const (
	defaultExploreSteps  = 8
	defaultExploreTokens = 8000
)

const exploreSystem = `You are a read-only subagent of Sigma. Investigate the task and return a concise summary.

You may call read_file, list_dir, and grep. You cannot edit files, run bash, or spawn another subagent.
When you have enough to answer, stop calling tools and reply with the summary: what you found, file paths, and anything the parent should do next. Do not include a plan to edit files yourself.`

// Explore runs a child agent on task and returns its summary. The child has
// its own message list, only the read-only tools, and a step and token budget.
// Progress is reported on the parent hooks as subagent events. The child does
// not write to the parent session log.
func (a *Agent) Explore(ctx context.Context, task string) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", errEmptyTask
	}
	if a == nil || a.Client == nil || a.Registry == nil {
		return "", errNoExplore
	}
	id := a.nextSubID()
	a.Hooks.subagent(SubagentEvent{Phase: "start", ID: id, Text: task})

	steps := a.ExploreMaxSteps
	if steps <= 0 {
		steps = defaultExploreSteps
	}
	budget := a.ExploreMaxTokens
	if budget <= 0 {
		budget = defaultExploreTokens
	}
	parentEnv := a.Registry.Env()
	childEnv := &tools.Env{Workspace: parentEnv.Workspace, Runner: parentEnv.Runner, BashTimeout: parentEnv.BashTimeout}
	policy, err := approval.New("plan", nil)
	if err != nil {
		return "", err
	}
	child := &Agent{
		Client:      a.Client,
		Registry:    tools.NewReadOnlyRegistry(childEnv),
		Policy:      policy,
		MaxSteps:    steps,
		TokenBudget: budget,
		Messages:    []provider.Message{{Role: "system", Content: exploreSystem}},
		Hooks: &Hooks{
			Step: func(n, max int) {
				a.Hooks.subagent(SubagentEvent{Phase: "step", ID: id, Step: n, Max: max})
			},
			ToolStart: func(ev ToolEvent) {
				a.Hooks.subagent(SubagentEvent{Phase: "step", ID: id, Text: ev.Name + " " + ev.Preview, Step: 0})
			},
			Usage: func(p, c, total int) { a.Hooks.usage(p, c, total) },
		},
	}
	runErr := child.RunTurn(ctx, task)
	summary := lastAssistantText(child.Messages)
	if strings.TrimSpace(summary) == "" && ctx.Err() == nil {
		s, serr := child.forceSummary(ctx)
		if serr == nil {
			summary = s
			runErr = nil
		} else if runErr == nil {
			runErr = serr
		}
	}
	failed := strings.TrimSpace(summary) == ""
	a.Hooks.subagent(SubagentEvent{Phase: "end", ID: id, Text: summary, Failed: failed})
	if failed {
		if runErr != nil {
			return "", runErr
		}
		return "", errNoSummary
	}
	return strings.TrimSpace(summary), nil
}

func (a *Agent) forceSummary(ctx context.Context) (string, error) {
	a.Messages = append(a.Messages, provider.Message{
		Role:    "user",
		Content: "Stop calling tools. Summarize what you found in one short paragraph.",
	})
	resp, err := a.Client.Complete(ctx, a.Messages, nil, nil)
	if err != nil {
		return "", err
	}
	a.Hooks.usage(resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	return strings.TrimSpace(resp.Content), nil
}

func lastAssistantText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && strings.TrimSpace(msgs[i].Content) != "" {
			return strings.TrimSpace(msgs[i].Content)
		}
	}
	return ""
}

func (a *Agent) nextSubID() string {
	n := a.subSeq.Add(1)
	return "sub_" + itoa(n)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
