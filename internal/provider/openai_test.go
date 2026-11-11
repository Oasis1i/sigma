package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamingToolCall(t *testing.T) {
	var gotAuth, gotModel, gotToolMessage bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Bearer test-key" {
			gotAuth = true
		}
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		if strings.Contains(text, `"model":"demo"`) {
			gotModel = true
		}
		if strings.Contains(text, `"stream":true`) && strings.Contains(text, `"tool_call_id":"c0"`) && strings.Contains(text, "read_file") {
			gotToolMessage = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"ba\",\"arguments\":\"\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"sh\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewOpenAI(srv.URL+"/v1", "test-key", "demo", srv.Client())
	var streamed strings.Builder
	resp, err := c.Complete(context.Background(), []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c0", Name: "read_file", Arguments: `{"path":"a"}`}}},
		{Role: "tool", ToolCallID: "c0", Name: "read_file", Content: "file"},
	}, []ToolSpec{{
		Name: "bash", Description: "run", Parameters: map[string]any{"type": "object"},
	}}, func(d string) { streamed.WriteString(d) })
	if err != nil {
		t.Fatal(err)
	}
	if !gotAuth || !gotModel || !gotToolMessage {
		t.Fatalf("auth %v model %v history %v", gotAuth, gotModel, gotToolMessage)
	}
	if resp.Content != "Hello" || streamed.String() != "Hello" {
		t.Fatalf("content %q streamed %q", resp.Content, streamed.String())
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "bash" || resp.ToolCalls[0].ID != "c1" {
		t.Fatalf("calls %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments != `{"command":"ls"}` {
		t.Fatalf("args %s", resp.ToolCalls[0].Arguments)
	}
	if resp.Usage.TotalTokens != 5 || resp.Usage.PromptTokens != 3 || resp.Usage.CompletionTokens != 2 {
		t.Fatalf("usage %+v", resp.Usage)
	}
}

func TestNonStreamObjectArgumentsAndError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok","tool_calls":[{"id":"t","function":{"name":"write_file","arguments":{"path":"a.txt","content":"hi"}}}]}}]}`)
	}))
	defer srv.Close()
	c := NewOpenAI(srv.URL, "", "m", srv.Client())
	resp, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || len(resp.ToolCalls) != 1 {
		t.Fatalf("%+v", resp)
	}
	if !strings.Contains(resp.ToolCalls[0].Arguments, `"path":"a.txt"`) {
		t.Fatalf("args %s", resp.ToolCalls[0].Arguments)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer bad.Close()
	c = NewOpenAI(bad.URL, "k", "m", bad.Client())
	_, err = c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("got %v", err)
	}
}
