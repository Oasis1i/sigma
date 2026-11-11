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

func TestGeminiOnboardAndStream(t *testing.T) {
	var onboard int
	var gen map[string]any
	var sawAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1internal:loadCodeAssist", func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`)
	})
	mux.HandleFunc("/v1internal:onboardUser", func(w http.ResponseWriter, r *http.Request) {
		onboard++
		io.WriteString(w, `{"done":true,"response":{"cloudaProject":{"id":"projects/9"}}}`)
	})
	mux.HandleFunc("/v1internal:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gen)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hi\"},{\"functionCall\":{\"name\":\"read_file\",\"args\":{\"path\":\"a.go\"}}}]}}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":3,\"totalTokenCount\":5}}}\n\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	store := auth.Store{Dir: t.TempDir()}
	if err := store.Save(auth.Token{Provider: "gemini", AccessToken: "g-access", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	src := &auth.Source{Name: "gemini", Spec: auth.GeminiSpec(), Store: store, Skew: time.Minute}
	client := NewGemini("gemini-2.5-pro", srv.URL+"/v1internal", src, srv.Client())
	resp, err := client.Complete(context.Background(), []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "go"},
	}, []ToolSpec{{Name: "read_file", Description: "read"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer g-access" || onboard != 1 {
		t.Fatalf("auth %s onboard %d", sawAuth, onboard)
	}
	if gen["project"] != "projects/9" || gen["model"] != "gemini-2.5-pro" {
		t.Fatalf("body %#v", gen)
	}
	req := gen["request"].(map[string]any)
	if req["systemInstruction"] == nil {
		t.Fatal("missing system")
	}
	decls := req["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)
	if decls[0].(map[string]any)["name"] != "read_file" {
		t.Fatalf("tools %#v", decls)
	}
	if resp.Content != "hi" || len(resp.ToolCalls) != 1 || !strings.Contains(resp.ToolCalls[0].Arguments, "a.go") {
		t.Fatalf("%+v", resp)
	}
	if resp.Usage.TotalTokens != 5 {
		t.Fatalf("usage %+v", resp.Usage)
	}
	saved, err := store.Load("gemini")
	if err != nil || saved.Extra["project"] != "projects/9" {
		t.Fatalf("stored %+v %v", saved, err)
	}
}

func TestGrokOAuthRefreshOn401(t *testing.T) {
	var authz []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz = append(authz, r.Header.Get("Authorization"))
		if len(authz) == 1 {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n")
	}))
	defer api.Close()
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new", "refresh_token": "r2", "expires_in": 60})
	}))
	defer tokens.Close()
	store := auth.Store{Dir: t.TempDir()}
	if err := store.Save(auth.Token{Provider: "grok", AccessToken: "old", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	spec := auth.GrokSpec()
	spec.TokenURL = tokens.URL
	src := &auth.Source{Name: "grok", Spec: spec, Store: store, HTTP: tokens.Client(), Skew: time.Minute}
	client := NewGrokOAuth("grok-2", api.URL+"/v1", src, api.Client())
	resp, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || authz[0] != "Bearer old" || authz[1] != "Bearer new" {
		t.Fatalf("%q %v", resp.Content, authz)
	}
}
