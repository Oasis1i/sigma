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
	"time"
)

// OpenAI is a streaming chat-completions client for any server that speaks
// the OpenAI wire format (official API, vLLM, llama.cpp, and most proxies).
type OpenAI struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client

	mu sync.Mutex
}

// NewOpenAI returns a client. httpClient may be nil.
func NewOpenAI(baseURL, apiKey, model string, httpClient *http.Client) *OpenAI {
	if httpClient == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		var transport *http.Transport
		if ok {
			transport = base.Clone()
		} else {
			transport = &http.Transport{}
		}
		// Local models can sit a while before the first byte. There is no
		// overall timeout: a stream may legitimately run for a long time.
		transport.ResponseHeaderTimeout = 5 * time.Minute
		httpClient = &http.Client{Transport: transport}
	}
	return &OpenAI{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		HTTP:    httpClient,
	}
}

type apiMessage struct {
	Role       string        `json:"role"`
	Content    *string       `json:"content"`
	Name       string        `json:"name,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
}

type apiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type apiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

// SetModel implements ModelSwitch.
func (c *OpenAI) SetModel(name string) {
	c.mu.Lock()
	c.Model = name
	c.mu.Unlock()
}

// CurrentModel implements ModelSwitch.
func (c *OpenAI) CurrentModel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Model
}

// Complete implements Client.
func (c *OpenAI) Complete(ctx context.Context, messages []Message, tools []ToolSpec, onDelta func(string)) (Response, error) {
	payload := map[string]any{
		"model":    c.CurrentModel(),
		"messages": toAPIMessages(messages),
		"stream":   true,
	}
	if len(tools) > 0 {
		payload["tools"] = toAPITools(tools)
		payload["tool_choice"] = "auto"
	}
	// Ask for a usage object on the final stream chunk. Servers that do not
	// know the field ignore it; servers that reject unknown fields are rare
	// among OpenAI-compatible APIs. The status bar shows the numbers only
	// when they actually arrive.
	payload["stream_options"] = map[string]any{"include_usage": true}
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, fmt.Errorf("encode request: %w", err)
	}
	endpoint := c.endpoint()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("User-Agent", "sigma")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("request %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return Response{}, rateLimitError("openai", resp)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		msg := strings.TrimSpace(string(b))
		if parsed := parseAPIError(b); parsed != "" {
			msg = parsed
		}
		if msg == "" {
			msg = resp.Status
		}
		return Response{}, fmt.Errorf("provider returned %s: %s", resp.Status, msg)
	}
	return decodeCompletion(resp.Body, resp.Header.Get("Content-Type"), onDelta)
}

func (c *OpenAI) endpoint() string {
	base := strings.TrimRight(c.BaseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

func toAPIMessages(messages []Message) []apiMessage {
	out := make([]apiMessage, 0, len(messages))
	for _, m := range messages {
		content := m.Content
		api := apiMessage{
			Role:       m.Role,
			Content:    &content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			args := tc.Arguments
			if args == "" {
				args = "{}"
			}
			call := apiToolCall{ID: tc.ID, Type: "function"}
			call.Function.Name = tc.Name
			call.Function.Arguments = args
			api.ToolCalls = append(api.ToolCalls, call)
		}
		out = append(out, api)
	}
	return out
}

func toAPITools(tools []ToolSpec) []apiTool {
	out := make([]apiTool, 0, len(tools))
	for _, t := range tools {
		params := t.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		item := apiTool{Type: "function"}
		item.Function.Name = t.Name
		item.Function.Description = t.Description
		item.Function.Parameters = params
		out = append(out, item)
	}
	return out
}

// decodeCompletion accepts either an SSE stream or a single JSON body.
// Some compatible servers ignore stream:true and return one JSON object.
func decodeCompletion(r io.Reader, contentType string, onDelta func(string)) (Response, error) {
	br := bufio.NewReader(r)
	if strings.Contains(contentType, "text/event-stream") {
		return parseSSE(br, onDelta)
	}
	for {
		b, err := br.ReadByte()
		if err != nil {
			if err == io.EOF {
				return Response{}, fmt.Errorf("provider returned an empty body")
			}
			return Response{}, fmt.Errorf("read response: %w", err)
		}
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if err := br.UnreadByte(); err != nil {
			return Response{}, err
		}
		if b == '{' {
			body, err := io.ReadAll(br)
			if err != nil {
				return Response{}, fmt.Errorf("read response: %w", err)
			}
			return parseJSONCompletion(body, onDelta)
		}
		return parseSSE(br, onDelta)
	}
}

type toolAcc struct {
	id   string
	name string
	args strings.Builder
}

type usagePayload struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string           `json:"content"`
			ToolCalls []streamToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	// Usage is often sent in a chunk whose choices array is empty.
	Usage *usagePayload   `json:"usage"`
	Error json.RawMessage `json:"error"`
}

type streamToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string     `json:"name"`
		Arguments flexString `json:"arguments"`
	} `json:"function"`
}

func parseSSE(r io.Reader, onDelta func(string)) (Response, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var content strings.Builder
	acc := map[int]*toolAcc{}
	var order []int
	var usage Usage
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Response{}, fmt.Errorf("stream decode: %w", err)
		}
		if msg := rawError(chunk.Error); msg != "" {
			return Response{}, fmt.Errorf("provider: %s", msg)
		}
		if chunk.Usage != nil {
			usage = Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			content.WriteString(delta.Content)
			if onDelta != nil {
				onDelta(delta.Content)
			}
		}
		for _, tc := range delta.ToolCalls {
			a, ok := acc[tc.Index]
			if !ok {
				a = &toolAcc{}
				acc[tc.Index] = a
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				a.id = tc.ID
			}
			if tc.Function.Name != "" {
				a.name += tc.Function.Name
			}
			if tc.Function.Arguments.Value != "" {
				a.args.WriteString(tc.Function.Arguments.Value)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, fmt.Errorf("read stream: %w", err)
	}
	return Response{Content: content.String(), ToolCalls: finishCalls(acc, order), Usage: usage}, nil
}

type jsonCompletion struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string     `json:"name"`
					Arguments flexString `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *usagePayload   `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func parseJSONCompletion(body []byte, onDelta func(string)) (Response, error) {
	if msg := parseAPIError(body); msg != "" {
		var probe jsonCompletion
		_ = json.Unmarshal(body, &probe)
		if len(probe.Choices) == 0 {
			return Response{}, fmt.Errorf("provider: %s", msg)
		}
	}
	var parsed jsonCompletion
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		if msg := rawError(parsed.Error); msg != "" {
			return Response{}, fmt.Errorf("provider: %s", msg)
		}
		return Response{}, fmt.Errorf("provider returned no choices")
	}
	msg := parsed.Choices[0].Message
	if msg.Content != "" && onDelta != nil {
		onDelta(msg.Content)
	}
	var calls []ToolCall
	for i, tc := range msg.ToolCalls {
		args := tc.Function.Arguments.Value
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
		}
		calls = append(calls, ToolCall{ID: id, Name: tc.Function.Name, Arguments: args})
	}
	resp := Response{Content: msg.Content, ToolCalls: calls}
	if parsed.Usage != nil {
		resp.Usage = Usage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		}
	}
	return resp, nil
}

func finishCalls(acc map[int]*toolAcc, order []int) []ToolCall {
	// Indexes usually arrive in order; sort so a late index 0 cannot land last.
	if len(order) > 1 {
		sorted := append([]int(nil), order...)
		for i := 1; i < len(sorted); i++ {
			for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
				sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			}
		}
		order = sorted
	}
	calls := make([]ToolCall, 0, len(order))
	for n, idx := range order {
		a := acc[idx]
		args := a.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		id := a.id
		if id == "" {
			id = fmt.Sprintf("call_%d", n+1)
		}
		calls = append(calls, ToolCall{ID: id, Name: a.name, Arguments: args})
	}
	return calls
}

// flexString accepts either a JSON string or a raw JSON value. A few
// compatible servers send tool arguments as an object instead of a string.
type flexString struct {
	Value string
}

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		f.Value = ""
		return nil
	}
	if b[0] == '"' {
		return json.Unmarshal(b, &f.Value)
	}
	f.Value = string(b)
	return nil
}

func parseAPIError(body []byte) string {
	var wrap struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return ""
	}
	return rawError(wrap.Error)
}

func rawError(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	return ""
}
