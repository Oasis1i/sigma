package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExchangeCodeSendsPKCEForm(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			t.Errorf("content-type %s", ct)
		}
		body, _ := io.ReadAll(r.Body)
		got, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"id_token":      jwtWithAccount("acct_9", "ada@example.com"),
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	}))
	defer srv.Close()
	spec := ChatGPTSpec()
	spec.TokenURL = srv.URL
	tok, err := ExchangeCode(context.Background(), srv.Client(), spec, "code-1", spec.RedirectURI, "verifier-1", "state-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("grant_type") != "authorization_code" || got.Get("client_id") != ChatGPTClientID || got.Get("code") != "code-1" || got.Get("code_verifier") != "verifier-1" {
		t.Fatalf("form %v", got)
	}
	if got.Get("redirect_uri") != ChatGPTRedirect {
		t.Fatalf("redirect %s", got.Get("redirect_uri"))
	}
	if tok.AccessToken != "access-1" || tok.AccountID != "acct_9" || tok.Email != "ada@example.com" {
		t.Fatalf("%+v", tok)
	}
	if tok.Expiry.Before(time.Now().Add(30 * time.Minute)) {
		t.Fatalf("expiry %s", tok.Expiry)
	}
}

func TestCallbackAndPaste(t *testing.T) {
	code, state, err := ParseCallbackInput("http://localhost:1455/auth/callback?code=abc&state=xyz")
	if err != nil || code != "abc" || state != "xyz" {
		t.Fatalf("url: %s %s %v", code, state, err)
	}
	code, state, err = ParseCallbackInput("raw-code#from-fragment")
	if err != nil || code != "raw-code" || state != "from-fragment" {
		t.Fatalf("fragment: %s %s %v", code, state, err)
	}
	spec := ChatGPTSpec()
	authURL, err := spec.AuthURLWith("st", PKCE{Challenge: "chal"}, spec.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != ChatGPTClientID || q.Get("code_challenge_method") != "S256" || q.Get("codex_cli_simplified_flow") != "true" {
		t.Fatalf("query %v", q)
	}
}

func TestLoginPasteStoresToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-paste",
			"refresh_token": "refresh-paste",
			"expires_in":    60,
		})
	}))
	defer srv.Close()
	spec := ChatGPTSpec()
	spec.TokenURL = srv.URL
	// Occupy the redirect port so login falls through to paste. Use a
	// redirect this test can bind... actually ChatGPT's port may be free.
	// Force paste by feeding stdin immediately; the callback can also win.
	// Point the redirect at an address we do not need, by using a listener
	// collision: pick the spec redirect only if we replace it with a closed
	// port. Binding 127.0.0.1:1 is not reliable. Feed the code on stdin and
	// let whichever path wins exchange the same code.
	store := Store{Dir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out strings.Builder
	tok, err := Login(ctx, spec, store, LoginOptions{
		In:   strings.NewReader("from-paste\n"),
		Out:  &out,
		HTTP: srv.Client(),
	})
	if err != nil {
		t.Fatal(err, out.String())
	}
	if tok.AccessToken != "access-paste" {
		t.Fatalf("%+v", tok)
	}
	if !strings.Contains(out.String(), "auth.openai.com/oauth/authorize") {
		t.Fatalf("output %s", out.String())
	}
	if strings.Contains(out.String(), "access-paste") || strings.Contains(out.String(), "refresh-paste") {
		t.Fatal("login output contains a token")
	}
	loaded, err := store.Load("chatgpt")
	if err != nil || loaded.AccessToken != "access-paste" {
		t.Fatalf("stored %+v %v", loaded, err)
	}
}

func TestSourceRefreshesWhenExpired(t *testing.T) {
	var grants []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		grants = append(grants, form.Get("grant_type"))
		if strings.Contains(string(body), "refresh-old") && !strings.Contains(string(body), "other") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-new",
				"refresh_token": "refresh-new",
				"expires_in":    120,
			})
			return
		}
		http.Error(w, "no", http.StatusBadRequest)
	}))
	defer srv.Close()
	dir := t.TempDir()
	store := Store{Dir: dir}
	if err := store.Save(Token{
		Provider:     "chatgpt",
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		Expiry:       time.Now().Add(-time.Minute),
		AccountID:    "acct_keep",
	}); err != nil {
		t.Fatal(err)
	}
	spec := ChatGPTSpec()
	spec.TokenURL = srv.URL
	src := &Source{Name: "chatgpt", Spec: spec, Store: store, HTTP: srv.Client(), Skew: time.Minute}
	tok, err := src.Token(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access-new" || tok.AccountID != "acct_keep" {
		t.Fatalf("%+v", tok)
	}
	if len(grants) != 1 || grants[0] != "refresh_token" {
		t.Fatalf("grants %v", grants)
	}
	fresh, err := src.Token(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.AccessToken != "access-new" || len(grants) != 1 {
		t.Fatalf("refreshed again: %+v grants %v", fresh, grants)
	}
}

func TestClaudeExchangeIsJSON(t *testing.T) {
	var raw []byte
	var ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct = r.Header.Get("Content-Type")
		raw, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "claude-access", "refresh_token": "claude-refresh", "expires_in": 10,
		})
	}))
	defer srv.Close()
	spec := ClaudeSpec()
	spec.TokenURL = srv.URL
	_, err := ExchangeCode(context.Background(), srv.Client(), spec, "c", spec.RedirectURI, "v", "state-9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type %s", ct)
	}
	if !strings.Contains(string(raw), `"state":"state-9"`) || !strings.Contains(string(raw), ClaudeClientID) {
		t.Fatalf("body %s", raw)
	}
}

func TestGeminiExchangeSendsClientSecret(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "g", "refresh_token": "r", "expires_in": 10})
	}))
	defer srv.Close()
	spec := GeminiSpec()
	spec.TokenURL = srv.URL
	spec.RedirectURI = "http://127.0.0.1:1/callback"
	spec.ClientID = "test-client"
	spec.ClientSecret = "test-secret"
	if _, err := ExchangeCode(context.Background(), srv.Client(), spec, "c", spec.RedirectURI, "v", "s"); err != nil {
		t.Fatal(err)
	}
	if form.Get("client_secret") != "test-secret" || form.Get("client_id") != "test-client" {
		t.Fatalf("form %v", form)
	}
}

func jwtWithAccount(account, email string) string {
	payload := map[string]any{
		"email": email,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": account,
		},
	}
	body, _ := json.Marshal(payload)
	return "h." + base64.RawURLEncoding.EncodeToString(body) + ".s"
}
