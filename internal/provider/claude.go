package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Oasis1i/sigma/internal/auth"
)

// Claude calls Anthropic /v1/messages with a Claude Code OAuth bearer.
// The system prompt and the last tool carry cache_control so prompt caching
// can apply. Header names match the public Claude Code client. A live
// account was not used to confirm that Anthropic still accepts them.
type Claude struct {
	Model     string
	Endpoint  string
	MaxTokens int
	Source    *auth.Source
	HTTP      *http.Client

	mu sync.Mutex
}

// NewClaude returns a client. endpoint may be empty for the public Messages URL.
func NewClaude(model, endpoint string, source *auth.Source, httpClient *http.Client) *Claude {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		endpoint = auth.ClaudeMessages
	}
	return &Claude{Model: model, Endpoint: endpoint, MaxTokens: 8192, Source: source, HTTP: httpClient}
}

// SetModel implements ModelSwitch.
func (c *Claude) SetModel(name string) {
	c.mu.Lock()
	c.Model = name
	c.mu.Unlock()
}

// CurrentModel implements ModelSwitch.
func (c *Claude) CurrentModel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Model
}

// Complete implements Client.
func (c *Claude) Complete(ctx context.Context, messages []Message, tools []ToolSpec, onDelta func(string)) (Response, error) {
	body, err := claudeBody(c.CurrentModel(), c.MaxTokens, messages, tools)
	if err != nil {
		return Response{}, err
	}
	resp, tok, err := doAuthed(ctx, c.Source, func(tok auth.Token) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		req.Header.Set("anthropic-version", "2023-06-01")
		// oauth-2025-04-20 is what Claude Code sends so an OAuth bearer is
		// accepted. prompt-caching marks cache_control. Not verified live.
		req.Header.Set("anthropic-beta", "oauth-2025-04-20,prompt-caching-2024-07-31,claude-code-20250219")
		req.Header.Set("User-Agent", "sigma")
		res, err := c.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("claude request: %w", err)
		}
		return res, nil
	})
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return Response{}, rateLimitError("claude", resp)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return Response{}, fmt.Errorf("claude returned %s: %s", resp.Status, redactToken(strings.TrimSpace(string(b)), tok.AccessToken))
	}
	return parseClaudeSSE(resp.Body, onDelta)
}

func claudeBody(model string, maxTokens int, messages []Message, tools []ToolSpec) ([]byte, error) {
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	system, msgs := claudeMessages(messages)
	payload := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"stream":     true,
		"messages":   msgs,
	}
	if system != "" {
		payload["system"] = []any{map[string]any{
			"type":          "text",
			"text":          system,
			"cache_control": map[string]string{"type": "ephemeral"},
		}}
	}
	if len(tools) > 0 {
		payload["tools"] = claudeTools(tools)
	}
	return json.Marshal(payload)
}

func claudeMessages(messages []Message) (string, []any) {
	var system []string
	var out []any
	var toolResults []any
	flush := func() {
		if len(toolResults) == 0 {
			return
		}
		out = append(out, map[string]any{"role": "user", "content": toolResults})
		toolResults = nil
	}
	for _, m := range messages {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				system = append(system, m.Content)
			}
		case "tool":
			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			})
		case "assistant":
			flush()
			blocks := claudeAssistant(m)
			out = append(out, map[string]any{"role": "assistant", "content": blocks})
		default:
			flush()
			out = append(out, map[string]any{"role": "user", "content": m.Content})
		}
	}
	flush()
	if out == nil {
		out = []any{}
	}
	return strings.Join(system, "\n\n"), out
}

func claudeAssistant(m Message) []any {
	var blocks []any
	if m.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
	}
	for _, tc := range m.ToolCalls {
		var input any
		if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil || input == nil {
			input = map[string]any{}
		}
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": input,
		})
	}
	if len(blocks) == 0 {
		blocks = []any{map[string]any{"type": "text", "text": ""}}
	}
	return blocks
}

func claudeTools(tools []ToolSpec) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		params := t.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": params,
		})
	}
	if len(out) > 0 {
		last := out[len(out)-1].(map[string]any)
		last["cache_control"] = map[string]string{"type": "ephemeral"}
	}
	return out
}

func parseClaudeSSE(r io.Reader, onDelta func(string)) (Response, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out Response
	type call struct {
		id, name string
		args     strings.Builder
	}
	calls := map[int]*call{}
	var order []int
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return Response{}, fmt.Errorf("claude stream: %w", err)
		}
		switch ev["type"] {
		case "message_start":
			if msg, _ := ev["message"].(map[string]any); msg != nil {
				if usage, _ := msg["usage"].(map[string]any); usage != nil {
					out.Usage.PromptTokens += intNum(usage["input_tokens"])
					out.Usage.PromptTokens += intNum(usage["cache_read_input_tokens"])
					out.Usage.PromptTokens += intNum(usage["cache_creation_input_tokens"])
				}
			}
		case "content_block_start":
			idx := intNum(ev["index"])
			block, _ := ev["content_block"].(map[string]any)
			if block == nil || block["type"] != "tool_use" {
				break
			}
			c := &call{}
			c.id, _ = block["id"].(string)
			c.name, _ = block["name"].(string)
			calls[idx] = c
			order = append(order, idx)
		case "content_block_delta":
			delta, _ := ev["delta"].(map[string]any)
			if delta == nil {
				break
			}
			switch delta["type"] {
			case "text_delta":
				text, _ := delta["text"].(string)
				out.Content += text
				if onDelta != nil && text != "" {
					onDelta(text)
				}
			case "input_json_delta":
				idx := intNum(ev["index"])
				c := calls[idx]
				if c == nil {
					c = &call{}
					calls[idx] = c
					order = append(order, idx)
				}
				part, _ := delta["partial_json"].(string)
				c.args.WriteString(part)
			}
		case "message_delta":
			if usage, _ := ev["usage"].(map[string]any); usage != nil {
				out.Usage.CompletionTokens += intNum(usage["output_tokens"])
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, fmt.Errorf("claude stream: %w", err)
	}
	for _, idx := range order {
		c := calls[idx]
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: c.id, Name: c.name, Arguments: c.args.String()})
	}
	out.Usage.TotalTokens = out.Usage.PromptTokens + out.Usage.CompletionTokens
	return out, nil
}
