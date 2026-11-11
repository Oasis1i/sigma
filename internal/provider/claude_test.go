package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Oasis1i/sigma/internal/auth"
)

func TestClaudeMapsToolsAndCaches(t *testing.T) {
	var hdr http.Header
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4,\"cache_read_input_tokens\":1}}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"read_file\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"a.go\\\"}\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\n")
	}))
	defer srv.Close()
	src := savedSource(t, "claude", "claude-access", time.Hour)
	client := NewClaude("claude-sonnet", srv.URL, src, srv.Client())
	var delta string
	resp, err := client.Complete(context.Background(), []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: "ok", ToolCalls: []ToolCall{{ID: "toolu_0", Name: "read_file", Arguments: `{"path":"b.go"}`}}},
		{Role: "tool", ToolCallID: "toolu_0", Content: "file text"},
	}, []ToolSpec{{Name: "read_file", Description: "read"}, {Name: "grep", Description: "grep"}}, func(s string) { delta += s })
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Authorization") != "Bearer claude-access" || hdr.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("headers %v", hdr)
	}
	if !strings.Contains(hdr.Get("anthropic-beta"), "oauth-2025-04-20") {
		t.Fatalf("beta %s", hdr.Get("anthropic-beta"))
	}
	system := body["system"].([]any)[0].(map[string]any)
	if system["text"] != "sys" || system["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("system %#v", system)
	}
	tools := body["tools"].([]any)
	last := tools[len(tools)-1].(map[string]any)
	if last["name"] != "grep" || last["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("tools %#v", tools)
	}
	msgs := body["messages"].([]any)
	asst := msgs[1].(map[string]any)
	blocks := asst["content"].([]any)
	foundUse := false
	for _, block := range blocks {
		obj := block.(map[string]any)
		if obj["type"] == "tool_use" && obj["name"] == "read_file" {
			foundUse = true
			if _, ok := obj["input"].(map[string]any); !ok {
				t.Fatalf("input %#v", obj["input"])
			}
		}
	}
	if !foundUse {
		t.Fatalf("messages %#v", msgs)
	}
	resultMsg := msgs[2].(map[string]any)
	results := resultMsg["content"].([]any)
	if results[0].(map[string]any)["type"] != "tool_result" {
		t.Fatalf("result %#v", results)
	}
	if resp.Content != "hi" || delta != "hi" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("%+v", resp)
	}
	if resp.Usage.PromptTokens != 5 || resp.Usage.CompletionTokens != 2 {
		t.Fatalf("usage %+v", resp.Usage)
	}
}

func TestClaudeRefreshOn401(t *testing.T) {
	var calls int
	var authz []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		authz = append(authz, r.Header.Get("Authorization"))
		if calls == 1 {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
	}))
	defer api.Close()
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new", "refresh_token": "r2", "expires_in": 3600})
	}))
	defer tokens.Close()
	store := auth.Store{Dir: t.TempDir()}
	if err := store.Save(auth.Token{Provider: "claude", AccessToken: "old", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	spec := auth.ClaudeSpec()
	spec.TokenURL = tokens.URL
	src := &auth.Source{Name: "claude", Spec: spec, Store: store, HTTP: tokens.Client(), Skew: time.Minute}
	client := NewClaude("m", api.URL, src, api.Client())
	resp, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || authz[0] != "Bearer old" || authz[1] != "Bearer new" {
		t.Fatalf("content %q auth %v", resp.Content, authz)
	}
}

func savedSource(t *testing.T, provider, access string, life time.Duration) *auth.Source {
	t.Helper()
	store := auth.Store{Dir: t.TempDir()}
	if err := store.Save(auth.Token{Provider: provider, AccessToken: access, Expiry: time.Now().Add(life)}); err != nil {
		t.Fatal(err)
	}
	spec, _ := auth.Lookup(provider)
	return &auth.Source{Name: provider, Spec: spec, Store: store, Skew: time.Minute}
}
