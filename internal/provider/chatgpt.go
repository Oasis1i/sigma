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

// ChatGPT calls the Codex Responses endpoint with a ChatGPT subscription
// token. The wire format is the Responses API, not chat completions.
// Headers match the public Codex client: bearer, chatgpt-account-id, and
// originator. A live account was not used to confirm them.
type ChatGPT struct {
	Model    string
	Endpoint string
	Source   *auth.Source
	HTTP     *http.Client

	mu sync.Mutex
}

// NewChatGPT returns a client. endpoint may be empty for the public Codex URL.
func NewChatGPT(model, endpoint string, source *auth.Source, httpClient *http.Client) *ChatGPT {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 0}
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		endpoint = auth.ChatGPTResponses
	}
	return &ChatGPT{Model: model, Endpoint: endpoint, Source: source, HTTP: httpClient}
}

// SetModel implements ModelSwitch.
func (c *ChatGPT) SetModel(name string) {
	c.mu.Lock()
	c.Model = name
	c.mu.Unlock()
}

// CurrentModel implements ModelSwitch.
func (c *ChatGPT) CurrentModel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Model
}

type tokenFunc func(ctx context.Context, force bool) (auth.Token, error)

// Complete implements Client. A 401 refreshes the token once and retries.
func (c *ChatGPT) Complete(ctx context.Context, messages []Message, tools []ToolSpec, onDelta func(string)) (Response, error) {
	if c.Source == nil {
		return Response{}, fmt.Errorf("chatgpt: no token source")
	}
	return completeWith(ctx, c.HTTP, c.endpoint(), c.model(), messages, tools, onDelta, c.Source.Token)
}

func (c *ChatGPT) model() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Model
}

func (c *ChatGPT) endpoint() string {
	if strings.TrimSpace(c.Endpoint) == "" {
		return auth.ChatGPTResponses
	}
	return c.Endpoint
}

func completeWith(ctx context.Context, httpClient *http.Client, endpoint, model string, messages []Message, tools []ToolSpec, onDelta func(string), token tokenFunc) (Response, error) {
	body, err := codexBody(model, messages, tools)
	if err != nil {
		return Response{}, err
	}
	tok, err := token(ctx, false)
	if err != nil {
		return Response{}, err
	}
	resp, err := postCodex(ctx, httpClient, endpoint, tok, body)
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		tok, err = token(ctx, true)
		if err != nil {
			return Response{}, err
		}
		resp, err = postCodex(ctx, httpClient, endpoint, tok, body)
		if err != nil {
			return Response{}, err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return Response{}, rateLimitError("chatgpt", resp)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return Response{}, fmt.Errorf("chatgpt returned %s: %s", resp.Status, redactToken(msg, tok.AccessToken))
	}
	return parseCodexSSE(resp.Body, onDelta)
}

func postCodex(ctx context.Context, httpClient *http.Client, endpoint string, tok auth.Token, body []byte) (*http.Response, error) {
	account := tok.AccountID
	if account == "" {
		account, _ = auth.AccountFromToken(tok.AccessToken)
	}
	if account == "" {
		return nil, fmt.Errorf("chatgpt: token has no chatgpt account id; run sigma login chatgpt again")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("chatgpt-account-id", account)
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("User-Agent", "sigma")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chatgpt request: %w", err)
	}
	return resp, nil
}

func redactToken(msg, token string) string {
	if token != "" {
		msg = strings.ReplaceAll(msg, token, "[redacted]")
	}
	return msg
}

func codexBody(model string, messages []Message, tools []ToolSpec) ([]byte, error) {
	instructions, input := codexInput(messages)
	payload := map[string]any{
		"model":  model,
		"input":  input,
		"stream": true,
		"store":  false,
	}
	if instructions != "" {
		payload["instructions"] = instructions
	}
	if len(tools) > 0 {
		payload["tools"] = codexTools(tools)
	}
	return json.Marshal(payload)
}

func codexInput(messages []Message) (string, []any) {
	var instructions []string
	var input []any
	for _, m := range messages {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				instructions = append(instructions, m.Content)
			}
		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		default:
			role := m.Role
			if role != "user" && role != "assistant" {
				role = "user"
			}
			textType := "input_text"
			if role == "assistant" {
				textType = "output_text"
			}
			if strings.TrimSpace(m.Content) != "" || len(m.ToolCalls) == 0 {
				input = append(input, map[string]any{
					"type": "message",
					"role": role,
					"content": []any{map[string]any{
						"type": textType,
						"text": m.Content,
					}},
				})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if args == "" {
					args = "{}"
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Name,
					"arguments": args,
				})
			}
		}
	}
	if input == nil {
		input = []any{}
	}
	return strings.Join(instructions, "\n\n"), input
}

func codexTools(tools []ToolSpec) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		params := t.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  params,
		})
	}
	return out
}

func parseCodexSSE(r io.Reader, onDelta func(string)) (Response, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out Response
	type call struct {
		id, name, args string
	}
	calls := map[string]*call{}
	order := []string{}
	ensure := func(id string) *call {
		if id == "" {
			id = fmt.Sprintf("call_%d", len(order)+1)
		}
		c, ok := calls[id]
		if !ok {
			c = &call{id: id}
			calls[id] = c
			order = append(order, id)
		}
		return c
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return Response{}, fmt.Errorf("chatgpt stream: %w", err)
		}
		switch ev["type"] {
		case "response.output_text.delta":
			delta, _ := ev["delta"].(string)
			out.Content += delta
			if onDelta != nil && delta != "" {
				onDelta(delta)
			}
		case "response.output_item.added", "response.output_item.done":
			item, _ := ev["item"].(map[string]any)
			if item == nil || item["type"] != "function_call" {
				break
			}
			id, _ := item["call_id"].(string)
			if id == "" {
				id, _ = item["id"].(string)
			}
			c := ensure(id)
			if name, _ := item["name"].(string); name != "" {
				c.name = name
			}
			if args, _ := item["arguments"].(string); args != "" {
				c.args = args
			}
			if itemID, _ := item["id"].(string); itemID != "" && calls[itemID] == nil {
				calls[itemID] = c
			}
		case "response.function_call_arguments.delta":
			id, _ := ev["item_id"].(string)
			c := ensure(id)
			delta, _ := ev["delta"].(string)
			c.args += delta
		case "response.completed":
			if usage := nestedMap(ev, "response", "usage"); usage != nil {
				out.Usage = Usage{
					PromptTokens:     intNum(usage["input_tokens"]),
					CompletionTokens: intNum(usage["output_tokens"]),
					TotalTokens:      intNum(usage["total_tokens"]),
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, fmt.Errorf("chatgpt stream: %w", err)
	}
	for _, id := range order {
		c := calls[id]
		if c.name == "" && c.args == "" {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: c.id, Name: c.name, Arguments: c.args})
	}
	return out, nil
}

func nestedMap(root map[string]any, keys ...string) map[string]any {
	cur := root
	for _, k := range keys {
		next, _ := cur[k].(map[string]any)
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

func intNum(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
