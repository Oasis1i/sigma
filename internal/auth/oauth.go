package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tokenResponse is the common OAuth token JSON. Vendors add fields; unknown
// ones are ignored.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// AuthURL builds the authorization URL for a PKCE login.
func (s Spec) AuthURLWith(state string, pkce PKCE, redirect string) (string, error) {
	if s.AuthURL == "" {
		return "", fmt.Errorf("%s: authorize URL is empty", s.Name)
	}
	if pkce.Challenge == "" {
		return "", fmt.Errorf("%s: PKCE challenge is empty", s.Name)
	}
	q := url.Values{}
	q.Set("client_id", s.ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirect)
	q.Set("scope", s.Scope)
	q.Set("state", state)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	if s.Name == "claude" {
		// Claude Code sends this flag. Without it the authorize page rejects
		// the request. Verified from public client traces, not a live login.
		q.Set("code", "true")
	}
	for key, values := range s.ExtraAuth {
		for _, v := range values {
			q.Set(key, v)
		}
	}
	return s.AuthURL + "?" + q.Encode(), nil
}

// ExchangeCode trades an authorization code for tokens.
func ExchangeCode(ctx context.Context, httpClient *http.Client, spec Spec, code, redirect, verifier, state string) (Token, error) {
	body, contentType, err := spec.tokenBody(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     spec.ClientID,
		"code":          code,
		"redirect_uri":  redirect,
		"code_verifier": verifier,
		"state":         state,
	}, spec.SendState)
	if err != nil {
		return Token{}, err
	}
	return postToken(ctx, httpClient, spec, contentType, body, "")
}

// Refresh trades a refresh token for a new access token.
func Refresh(ctx context.Context, httpClient *http.Client, spec Spec, refreshToken string) (Token, error) {
	fields := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     spec.ClientID,
		"refresh_token": refreshToken,
	}
	if spec.RefreshScope != "" {
		fields["scope"] = spec.RefreshScope
	}
	body, contentType, err := spec.tokenBody(fields, false)
	if err != nil {
		return Token{}, err
	}
	tok, err := postToken(ctx, httpClient, spec, contentType, body, refreshToken)
	if err != nil {
		return Token{}, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

func (s Spec) tokenBody(fields map[string]string, includeState bool) (string, string, error) {
	if s.ClientSecret != "" {
		fields["client_secret"] = s.ClientSecret
	}
	if !includeState {
		delete(fields, "state")
	}
	if s.JSONToken {
		payload := map[string]string{}
		for k, v := range fields {
			if v != "" {
				payload[k] = v
			}
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", "", err
		}
		return string(raw), "application/json", nil
	}
	form := url.Values{}
	for k, v := range fields {
		if v != "" {
			form.Set(k, v)
		}
	}
	return form.Encode(), "application/x-www-form-urlencoded", nil
}

func postToken(ctx context.Context, httpClient *http.Client, spec Spec, contentType, body, previousRefresh string) (Token, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if spec.TokenURL == "" {
		return Token{}, fmt.Errorf("%s: token URL is empty", spec.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.TokenURL, strings.NewReader(body))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("%s: token request: %w", spec.Name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return Token{}, fmt.Errorf("%s: read token response: %w", spec.Name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Token{}, fmt.Errorf("%s: token endpoint returned %s: %s", spec.Name, resp.Status, redact(string(raw), previousRefresh))
	}
	var parsed tokenResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Token{}, fmt.Errorf("%s: parse token response: %w", spec.Name, err)
	}
	if parsed.AccessToken == "" {
		return Token{}, fmt.Errorf("%s: token response has no access_token", spec.Name)
	}
	account, email := identityFromJWTs(parsed.IDToken, parsed.AccessToken)
	expiry := time.Time{}
	if parsed.ExpiresIn > 0 {
		expiry = time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	}
	return Token{
		Provider:     spec.Name,
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		TokenType:    parsed.TokenType,
		Expiry:       expiry,
		AccountID:    account,
		Email:        email,
	}, nil
}

func redact(s string, secrets ...string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		s = s[:400]
	}
	for _, secret := range secrets {
		if len(secret) >= 6 {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}

// ParseCallbackInput accepts a raw code, a code#state fragment, or a full
// callback URL. The code is not logged by the caller.
func ParseCallbackInput(input string) (code, state string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("empty login code")
	}
	if strings.Contains(input, "://") {
		u, err := url.Parse(input)
		if err != nil {
			return "", "", fmt.Errorf("callback URL: %w", err)
		}
		q := u.Query()
		if msg := q.Get("error"); msg != "" {
			desc := q.Get("error_description")
			if desc != "" {
				msg = msg + ": " + desc
			}
			return "", "", fmt.Errorf("authorization error: %s", msg)
		}
		code = q.Get("code")
		state = q.Get("state")
		if code == "" {
			return "", "", fmt.Errorf("callback URL has no code")
		}
		return splitCodeState(code, state)
	}
	return splitCodeState(input, "")
}

func splitCodeState(code, state string) (string, string, error) {
	if i := strings.IndexByte(code, '#'); i >= 0 {
		if state == "" {
			state = code[i+1:]
		}
		code = code[:i]
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return "", "", fmt.Errorf("empty login code")
	}
	return code, state, nil
}

// LoginOptions controls the interactive login. In and Out default to the
// process standard streams when nil. HTTP defaults to http.DefaultClient.
type LoginOptions struct {
	In   io.Reader
	Out  io.Writer
	HTTP *http.Client
	Open func(string) error
	// Now is reserved for tests that need a fixed clock. Unused by Login.
	Now func() time.Time
}

// Login runs the PKCE flow. It listens on the redirect host when it can, and
// always accepts a pasted code or callback URL on In. The first of the two
// wins.
func Login(ctx context.Context, spec Spec, store Store, opt LoginOptions) (Token, error) {
	if opt.Out == nil {
		opt.Out = io.Discard
	}
	if opt.In == nil {
		opt.In = strings.NewReader("")
	}
	pkce, err := NewPKCE()
	if err != nil {
		return Token{}, err
	}
	state, err := NewState()
	if err != nil {
		return Token{}, err
	}
	redirect := spec.RedirectURI
	var ln net.Listener
	if redirect == "" {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return Token{}, fmt.Errorf("%s: listen for callback: %w", spec.Name, err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		redirect = fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	} else {
		ln, err = listenRedirect(redirect)
		if err != nil {
			fmt.Fprintf(opt.Out, "sigma: could not listen on %s (%v). Paste the callback URL instead.\n", redirect, err)
			ln = nil
		}
	}
	spec.RedirectURI = redirect
	if ln != nil {
		defer ln.Close()
	}
	authURL, err := spec.AuthURLWith(state, pkce, redirect)
	if err != nil {
		return Token{}, err
	}
	fmt.Fprintf(opt.Out, "Open this URL to sign in to %s:\n%s\n", spec.Name, authURL)
	fmt.Fprintln(opt.Out, "Waiting for the browser. Or paste the callback URL or the code, then press Enter.")
	if opt.Open != nil {
		if err := opt.Open(authURL); err != nil {
			fmt.Fprintf(opt.Out, "sigma: could not open a browser (%v). Use the URL above.\n", err)
		}
	}
	code, gotState, err := waitForCode(ctx, ln, redirect, state, opt.In)
	if err != nil {
		return Token{}, err
	}
	if gotState != "" && gotState != state {
		return Token{}, fmt.Errorf("%s: oauth state mismatch", spec.Name)
	}
	tok, err := ExchangeCode(ctx, opt.HTTP, spec, code, redirect, pkce.Verifier, state)
	if err != nil {
		return Token{}, err
	}
	if err := store.Save(tok); err != nil {
		return Token{}, err
	}
	fmt.Fprintf(opt.Out, "saved %s credentials for %s\n", spec.Name, emptyDash(tok.Email))
	return tok, nil
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no email in the token)"
	}
	return s
}

// Discover reads an OpenID configuration document and returns the
// authorization and token endpoints. Grok's login uses this. Tests pass a
// local URL; the real call is https://auth.x.ai/.well-known/openid-configuration.
func Discover(ctx context.Context, httpClient *http.Client, discoveryURL string) (authURL, tokenURL string, err error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("oidc discovery returned %s", resp.Status)
	}
	var doc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", "", fmt.Errorf("oidc discovery: %w", err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return "", "", fmt.Errorf("oidc discovery is missing an endpoint")
	}
	return doc.AuthorizationEndpoint, doc.TokenEndpoint, nil
}

func listenRedirect(redirect string) (net.Listener, error) {
	u, err := url.Parse(redirect)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("redirect %q has no host", redirect)
	}
	return net.Listen("tcp", u.Host)
}

func waitForCode(ctx context.Context, ln net.Listener, redirect, state string, in io.Reader) (string, string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type got struct {
		code, state string
		err         error
	}
	ch := make(chan got, 2)
	if ln != nil {
		path := "/"
		if u, err := url.Parse(redirect); err == nil && u.Path != "" {
			path = u.Path
		}
		srv := &http.Server{Handler: callbackHandler(path, state, func(code, st string, err error) {
			ch <- got{code, st, err}
			cancel()
		})}
		go func() {
			err := srv.Serve(ln)
			if err != nil && ctx.Err() == nil {
				ch <- got{err: err}
			}
		}()
		go func() {
			<-ctx.Done()
			_ = srv.Close()
		}()
	}
	go func() {
		line, err := readLine(in)
		if err != nil {
			if err != io.EOF {
				ch <- got{err: err}
			}
			return
		}
		if strings.TrimSpace(line) == "" {
			return
		}
		code, st, err := ParseCallbackInput(line)
		ch <- got{code, st, err}
		cancel()
	}()
	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	case g := <-ch:
		if g.err != nil {
			return "", "", g.err
		}
		return g.code, g.state, nil
	}
}

func callbackHandler(path, state string, done func(code, state string, err error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if msg := q.Get("error"); msg != "" {
			http.Error(w, "login failed", http.StatusBadRequest)
			done("", "", fmt.Errorf("authorization error: %s", msg))
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		if st := q.Get("state"); st != "" && st != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			done("", "", fmt.Errorf("oauth state mismatch"))
			return
		}
		fmt.Fprintln(w, "sigma login complete. You can close this tab.")
		done(code, q.Get("state"), nil)
	})
}

func readLine(r io.Reader) (string, error) {
	var buf [4096]byte
	var b strings.Builder
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			b.Write(buf[:n])
			if i := strings.IndexByte(b.String(), '\n'); i >= 0 {
				return b.String()[:i], nil
			}
		}
		if err != nil {
			if err == io.EOF && b.Len() > 0 {
				return b.String(), nil
			}
			return "", err
		}
	}
}
