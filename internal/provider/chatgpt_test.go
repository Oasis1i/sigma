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

func TestChatGPTRequestShapeAndStream(t *testing.T) {
	var got http.Request
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc1\",\"call_id\":\"call_1\",\"name\":\"read_file\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc1\",\"delta\":\"{\\\"path\\\":\\\"a.go\\\"}\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":4,\"total_tokens\":7}}}\n\n")
	}))
	defer srv.Close()

	store := auth.Store{Dir: t.TempDir()}
	if err := store.Save(auth.Token{
		Provider:    "chatgpt",
		AccessToken: "access-secret",
		AccountID:   "acct_1",
		Expiry:      time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	src := &auth.Source{Name: "chatgpt", Spec: auth.ChatGPTSpec(), Store: store, Skew: time.Minute}
	client := NewChatGPT("gpt-5.3-codex", srv.URL, src, srv.Client())
	var deltas string
	resp, err := client.Complete(context.Background(), []Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "read a.go"},
	}, []ToolSpec{{Name: "read_file", Description: "read", Parameters: map[string]any{"type": "object"}}}, func(s string) { deltas += s })
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("Authorization") != "Bearer access-secret" {
		t.Fatalf("auth %s", got.Header.Get("Authorization"))
	}
	if got.Header.Get("chatgpt-account-id") != "acct_1" || got.Header.Get("originator") != "codex_cli_rs" {
		t.Fatalf("headers account %s originator %s", got.Header.Get("chatgpt-account-id"), got.Header.Get("originator"))
	}
	if got.Header.Get("OpenAI-Beta") != "responses=experimental" {
		t.Fatalf("beta %s", got.Header.Get("OpenAI-Beta"))
	}
	if body["model"] != "gpt-5.3-codex" || body["stream"] != true || body["store"] != false || body["instructions"] != "be brief" {
		t.Fatalf("body %#v", body)
	}
	if resp.Content != "hi" || deltas != "hi" {
		t.Fatalf("text %q deltas %q", resp.Content, deltas)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" || resp.ToolCalls[0].ID != "call_1" || !strings.Contains(resp.ToolCalls[0].Arguments, "a.go") {
		t.Fatalf("tools %+v", resp.ToolCalls)
	}
	if resp.Usage.PromptTokens != 3 || resp.Usage.CompletionTokens != 4 || resp.Usage.TotalTokens != 7 {
		t.Fatalf("usage %+v", resp.Usage)
	}
}

func TestChatGPTRefreshOn401(t *testing.T) {
	var calls int
	var authz []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		authz = append(authz, r.Header.Get("Authorization"))
		if calls == 1 {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}))
	defer api.Close()
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "refresh-secret") {
			http.Error(w, "missing refresh", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-new",
			"refresh_token": "refresh-new",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()
	store := auth.Store{Dir: t.TempDir()}
	if err := store.Save(auth.Token{
		Provider:     "chatgpt",
		AccessToken:  "access-old",
		RefreshToken: "refresh-secret",
		AccountID:    "acct_1",
		Expiry:       time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	spec := auth.ChatGPTSpec()
	spec.TokenURL = tokenSrv.URL
	src := &auth.Source{Name: "chatgpt", Spec: spec, Store: store, HTTP: tokenSrv.Client(), Skew: time.Minute}
	client := NewChatGPT("m", api.URL, src, api.Client())
	resp, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || calls != 2 {
		t.Fatalf("content %q calls %d", resp.Content, calls)
	}
	if authz[0] != "Bearer access-old" || authz[1] != "Bearer access-new" {
		t.Fatalf("authz %v", authz)
	}
}

func TestChatGPTRateLimitMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "12")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	store := auth.Store{Dir: t.TempDir()}
	if err := store.Save(auth.Token{
		Provider: "chatgpt", AccessToken: "a", AccountID: "acct", Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	src := &auth.Source{Name: "chatgpt", Spec: auth.ChatGPTSpec(), Store: store}
	client := NewChatGPT("m", srv.URL, src, srv.Client())
	_, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "rate limited") || !strings.Contains(err.Error(), "resets at") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(err.Error(), "access") && strings.Contains(err.Error(), "Bearer") {
		t.Fatal(err)
	}
}
