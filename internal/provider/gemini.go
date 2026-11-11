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

// Gemini calls the Gemini CLI Code Assist endpoint (cloudcode-pa) with a
// Google OAuth token. The first request loads or onboards a project id, the
// same step gemini-cli performs, and stores it on the token. A live Google
// account was not used to confirm the JSON field names.
type Gemini struct {
	Model  string
	Base   string
	Source *auth.Source
	HTTP   *http.Client

	mu sync.Mutex
}

// NewGemini returns a client. base may be empty for the public Code Assist host.
func NewGemini(model, base string, source *auth.Source, httpClient *http.Client) *Gemini {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = auth.GeminiCodeAssist
	}
	return &Gemini{Model: model, Base: base, Source: source, HTTP: httpClient}
}

// SetModel implements ModelSwitch.
func (c *Gemini) SetModel(name string) {
	c.mu.Lock()
	c.Model = name
	c.mu.Unlock()
}

// CurrentModel implements ModelSwitch.
func (c *Gemini) CurrentModel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Model
}

// Complete implements Client.
func (c *Gemini) Complete(ctx context.Context, messages []Message, tools []ToolSpec, onDelta func(string)) (Response, error) {
	if c.Source == nil {
		return Response{}, errNoToken
	}
	tok, err := c.Source.Token(ctx, false)
	if err != nil {
		return Response{}, err
	}
	project := ""
	if tok.Extra != nil {
		project = tok.Extra["project"]
	}
	if project == "" {
		project, err = c.ensureProject(ctx, tok.AccessToken)
		if err != nil {
			return Response{}, err
		}
		if tok.Extra == nil {
			tok.Extra = map[string]string{}
		}
		tok.Extra["project"] = project
		if c.Source.Store.Dir != "" {
			_ = c.Source.Store.Save(tok)
		}
	}
	body, err := geminiBody(c.CurrentModel(), project, messages, tools)
	if err != nil {
		return Response{}, err
	}
	endpoint := c.Base + ":streamGenerateContent?alt=sse"
	resp, tok, err := doAuthed(ctx, c.Source, func(tok auth.Token) (*http.Response, error) {
		return c.post(ctx, endpoint, tok.AccessToken, body)
	})
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return Response{}, rateLimitError("gemini", resp)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return Response{}, fmt.Errorf("gemini returned %s: %s", resp.Status, redactToken(strings.TrimSpace(string(b)), tok.AccessToken))
	}
	return parseGeminiSSE(resp.Body, onDelta)
}

func (c *Gemini) ensureProject(ctx context.Context, access string) (string, error) {
	meta := geminiMetadata()
	raw, err := json.Marshal(map[string]any{"metadata": meta})
	if err != nil {
		return "", err
	}
	resp, err := c.post(ctx, c.Base+":loadCodeAssist", access, raw)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gemini loadCodeAssist returned %s", resp.Status)
	}
	if project := projectFrom(body); project != "" {
		return project, nil
	}
	tier := tierFrom(body)
	onboard, err := json.Marshal(map[string]any{
		"tierId":        tier,
		"cloudaProject": "",
		"metadata":      meta,
	})
	if err != nil {
		return "", err
	}
	for i := 0; i < 5; i++ {
		resp, err := c.post(ctx, c.Base+":onboardUser", access, onboard)
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		status := resp.StatusCode
		resp.Body.Close()
		if status < 200 || status >= 300 {
			return "", fmt.Errorf("gemini onboardUser returned %d", status)
		}
		var doc map[string]any
		_ = json.Unmarshal(body, &doc)
		if project := projectFrom(body); project != "" && doc["done"] != false {
			return project, nil
		}
		if done, ok := doc["done"].(bool); ok && !done {
			continue
		}
		if project := projectFrom(body); project != "" {
			return project, nil
		}
	}
	return "", fmt.Errorf("gemini: code assist did not return a project id")
}

func (c *Gemini) post(ctx context.Context, url, access string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("User-Agent", "sigma")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	return resp, nil
}

func geminiMetadata() map[string]string {
	return map[string]string{
		"ideType":    "IDE_UNSPECIFIED",
		"platform":   "PLATFORM_UNSPECIFIED",
		"pluginType": "GEMINI",
	}
}

func projectFrom(body []byte) string {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return ""
	}
	if id := projectValue(doc["cloudaProject"]); id != "" {
		return id
	}
	if resp, _ := doc["response"].(map[string]any); resp != nil {
		if id := projectValue(resp["cloudaProject"]); id != "" {
			return id
		}
	}
	return ""
}

func projectValue(v any) string {
	switch p := v.(type) {
	case string:
		return strings.TrimSpace(p)
	case map[string]any:
		id, _ := p["id"].(string)
		return strings.TrimSpace(id)
	default:
		return ""
	}
}

func tierFrom(body []byte) string {
	var doc struct {
		AllowedTiers []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"allowedTiers"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || len(doc.AllowedTiers) == 0 {
		return "free-tier"
	}
	for _, tier := range doc.AllowedTiers {
		if tier.IsDefault && tier.ID != "" {
			return tier.ID
		}
	}
	if doc.AllowedTiers[0].ID != "" {
		return doc.AllowedTiers[0].ID
	}
	return "free-tier"
}

func geminiBody(model, project string, messages []Message, tools []ToolSpec) ([]byte, error) {
	system, contents := geminiContents(messages)
	req := map[string]any{"contents": contents}
	if system != "" {
		req["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": system}}}
	}
	if len(tools) > 0 {
		decls := make([]any, 0, len(tools))
		for _, t := range tools {
			params := t.Parameters
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			decls = append(decls, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			})
		}
		req["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	return json.Marshal(map[string]any{
		"model":   model,
		"project": project,
		"request": req,
	})
}

func geminiContents(messages []Message) (string, []any) {
	var system []string
	var out []any
	names := map[string]string{}
	var pending []any
	flush := func() {
		if len(pending) == 0 {
			return
		}
		out = append(out, map[string]any{"role": "user", "parts": pending})
		pending = nil
	}
	for _, m := range messages {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				system = append(system, m.Content)
			}
		case "tool":
			name := names[m.ToolCallID]
			if name == "" {
				name = m.ToolCallID
			}
			pending = append(pending, map[string]any{
				"functionResponse": map[string]any{
					"name":     name,
					"response": map[string]any{"result": m.Content},
				},
			})
		case "assistant":
			flush()
			var parts []any
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				names[tc.ID] = tc.Name
				var args any
				if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil || args == nil {
					args = map[string]any{}
				}
				parts = append(parts, map[string]any{
					"functionCall": map[string]any{"name": tc.Name, "args": args},
				})
			}
			if len(parts) == 0 {
				parts = []any{map[string]any{"text": ""}}
			}
			out = append(out, map[string]any{"role": "model", "parts": parts})
		default:
			flush()
			out = append(out, map[string]any{
				"role":  "user",
				"parts": []any{map[string]any{"text": m.Content}},
			})
		}
	}
	flush()
	if out == nil {
		out = []any{}
	}
	return strings.Join(system, "\n\n"), out
}

func parseGeminiSSE(r io.Reader, onDelta func(string)) (Response, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out Response
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(data), &doc); err != nil {
			return Response{}, fmt.Errorf("gemini stream: %w", err)
		}
		payload := doc
		if inner, _ := doc["response"].(map[string]any); inner != nil {
			payload = inner
		}
		if usage, _ := payload["usageMetadata"].(map[string]any); usage != nil {
			out.Usage.PromptTokens = intNum(usage["promptTokenCount"])
			out.Usage.CompletionTokens = intNum(usage["candidatesTokenCount"])
			out.Usage.TotalTokens = intNum(usage["totalTokenCount"])
		}
		cands, _ := payload["candidates"].([]any)
		if len(cands) == 0 {
			continue
		}
		cand, _ := cands[0].(map[string]any)
		content, _ := cand["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for i, part := range parts {
			obj, _ := part.(map[string]any)
			if obj == nil {
				continue
			}
			if text, _ := obj["text"].(string); text != "" {
				out.Content += text
				if onDelta != nil {
					onDelta(text)
				}
			}
			call, _ := obj["functionCall"].(map[string]any)
			if call == nil {
				continue
			}
			name, _ := call["name"].(string)
			args := "{}"
			if raw, err := json.Marshal(call["args"]); err == nil && string(raw) != "null" {
				args = string(raw)
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        fmt.Sprintf("gemini_%d_%d", len(out.ToolCalls), i),
				Name:      name,
				Arguments: args,
			})
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, fmt.Errorf("gemini stream: %w", err)
	}
	return out, nil
}
