package gui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oasis1i/sigma/internal/agent"
	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/engine"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/sandbox"
	"github.com/Oasis1i/sigma/internal/session"
	"github.com/Oasis1i/sigma/internal/tools"
)

func testStack(t *testing.T, mode string, client provider.Client) (*engine.Engine, *Server, *httptest.Server) {
	t.Helper()
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sessDir := filepath.Join(root, "sessions")
	policy, err := approval.New(mode, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := session.Create(sessDir, map[string]string{"model": "fake"})
	if err != nil {
		t.Fatal(err)
	}
	ag := &agent.Agent{
		Client: client,
		Registry: tools.NewRegistry(&tools.Env{
			Workspace:   ws,
			Runner:      &sandbox.Local{Dir: ws},
			BashTimeout: 5 * time.Second,
		}),
		Policy:   policy,
		Session:  sess,
		Messages: []provider.Message{{Role: "system", Content: "sys"}},
		MaxSteps: 6,
	}
	if err := sess.AppendMessage(ag.Messages[0]); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(ag, engine.Options{
		SessionDir: sessDir,
		Model:      "fake-model",
		BaseURL:    "http://example.test/v1",
		Workspace:  ws,
	})
	guiSrv := New(eng, "test-token-0123456789abcdef", "0.2.0")
	httpSrv := httptest.NewServer(guiSrv.Handler())
	t.Cleanup(func() {
		httpSrv.Close()
		guiSrv.Close()
		eng.Close()
	})
	return eng, guiSrv, httpSrv
}

func TestTokenRequired(t *testing.T) {
	_, _, httpSrv := testStack(t, "auto", &provider.Scripted{})
	client := httpSrv.Client()

	res, err := client.Get(httpSrv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %s", res.Status)
	}
	res.Body.Close()

	res, err = client.Get(httpSrv.URL + "/api/state?token=wrong-token-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: %s", res.Status)
	}
	res.Body.Close()

	req, err := http.NewRequest(http.MethodGet, httpSrv.URL+"/api/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token-0123456789abcdef")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bearer: %s", res.Status)
	}
	var state stateJSON
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Model != "fake-model" || state.Workspace == "" || state.SessionID == "" {
		t.Fatalf("state %+v", state)
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("csp %q", res.Header.Get("Content-Security-Policy"))
	}

	res, err = client.Get(httpSrv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("events without token: %s", res.Status)
	}
	res.Body.Close()

	page, err := client.Get(httpSrv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	body, _ := io.ReadAll(page.Body)
	if page.StatusCode != http.StatusOK || !strings.Contains(string(body), "sigma") || !strings.Contains(string(body), "/app.js") {
		t.Fatalf("page %s %s", page.Status, body)
	}
}

func TestRejectsForeignHostAndOrigin(t *testing.T) {
	_, guiSrv, _ := testStack(t, "auto", &provider.Scripted{})
	req := httptest.NewRequest(http.MethodGet, "/api/state?token=test-token-0123456789abcdef", nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	guiSrv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host: %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/message", strings.NewReader(`{"text":"hi"}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer test-token-0123456789abcdef")
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	guiSrv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSSEAndApproval(t *testing.T) {
	script := &provider.Scripted{Responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "write_file", Arguments: `{"path":"nope.txt","content":"x"}`,
		}}},
		{Content: "stopped", Usage: provider.Usage{PromptTokens: 4, CompletionTokens: 1, TotalTokens: 5}},
	}}
	_, _, httpSrv := testStack(t, "ask", script)
	ws := workspaceFrom(t, httpSrv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpSrv.URL+"/api/events?token=test-token-0123456789abcdef", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := httpSrv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("events: %s", res.Status)
	}
	sc := bufio.NewScanner(res.Body)
	if !sc.Scan() || !strings.Contains(sc.Text(), "ok") {
		t.Fatalf("open frame: %q %v", sc.Text(), sc.Err())
	}

	post, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/api/message", strings.NewReader(`{"text":"write a file"}`))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Authorization", "Bearer test-token-0123456789abcdef")
	post.Header.Set("Content-Type", "application/json")
	posted, err := httpSrv.Client().Do(post)
	if err != nil {
		t.Fatal(err)
	}
	posted.Body.Close()
	if posted.StatusCode != http.StatusAccepted {
		t.Fatalf("post: %s", posted.Status)
	}

	var kinds []string
	var approvalID string
	deadline := time.After(4 * time.Second)
	for {
		lineCh := make(chan string, 1)
		go func() {
			if sc.Scan() {
				lineCh <- sc.Text()
			} else {
				lineCh <- ""
			}
		}()
		var line string
		select {
		case line = <-lineCh:
		case <-deadline:
			t.Fatalf("timeout, kinds %v", kinds)
		}
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev engine.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, string(ev.Kind))
		if ev.Kind == engine.KindApproval {
			approvalID = ev.ApprovalID
			if _, err := os.Stat(filepath.Join(ws, "nope.txt")); !os.IsNotExist(err) {
				t.Fatalf("file existed before the decision: %v", err)
			}
			deny, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/api/approval", strings.NewReader(`{"id":"`+approvalID+`","allow":false}`))
			if err != nil {
				t.Fatal(err)
			}
			deny.Header.Set("Authorization", "Bearer test-token-0123456789abcdef")
			deny.Header.Set("Content-Type", "application/json")
			dres, err := httpSrv.Client().Do(deny)
			if err != nil {
				t.Fatal(err)
			}
			dres.Body.Close()
			if dres.StatusCode != http.StatusNoContent {
				t.Fatalf("deny: %s", dres.Status)
			}
		}
		if ev.Kind == engine.KindTextDelta && ev.Text != "stopped" {
			t.Fatalf("delta %q", ev.Text)
		}
		if ev.Kind == engine.KindDone {
			break
		}
	}
	if approvalID == "" || !containsAll(kinds, "tool_start", "approval_request", "tool_end", "text_delta", "done") {
		t.Fatalf("kinds %v", kinds)
	}
	if _, err := os.Stat(filepath.Join(ws, "nope.txt")); !os.IsNotExist(err) {
		t.Fatal("denied write created a file")
	}
}

func TestListensOnLoopback(t *testing.T) {
	eng, _, _ := testStack(t, "auto", &provider.Scripted{})
	ln, err := Listen(eng, "another-token-0123456789abcdef", "0.2.0", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !strings.HasPrefix(ln.Addr(), "127.0.0.1:") {
		t.Fatalf("addr %s", ln.Addr())
	}
	if !strings.Contains(ln.URL(), "token=another-token-0123456789abcdef") {
		t.Fatalf("url %s", ln.URL())
	}
	if _, err := Listen(eng, "t", "v", 99999); err == nil {
		t.Fatal("expected a bad port to fail")
	}
}

func workspaceFrom(t *testing.T, httpSrv *httptest.Server) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, httpSrv.URL+"/api/state", nil)
	req.Header.Set("Authorization", "Bearer test-token-0123456789abcdef")
	res, err := httpSrv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var state stateJSON
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	return state.Workspace
}

func containsAll(got []string, want ...string) bool {
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
