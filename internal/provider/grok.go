package provider

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/Oasis1i/sigma/internal/auth"
)

// GrokOAuth calls the Grok CLI chat proxy with an OAuth bearer. The wire
// format is OpenAI chat completions. The official xAI API key path does not
// use this type: it is a normal OpenAI client pointed at https://api.x.ai/v1.
// Whether cli-chat-proxy.grok.com still accepts this client was not checked
// against a live Grok login.
type GrokOAuth struct {
	Model  string
	Base   string
	Source *auth.Source
	HTTP   *http.Client

	mu sync.Mutex
}

// NewGrokOAuth returns a client. base may be empty for the CLI chat proxy.
func NewGrokOAuth(model, base string, source *auth.Source, httpClient *http.Client) *GrokOAuth {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = auth.GrokCLIProxyBase
	}
	return &GrokOAuth{Model: model, Base: base, Source: source, HTTP: httpClient}
}

// SetModel implements ModelSwitch.
func (g *GrokOAuth) SetModel(name string) {
	g.mu.Lock()
	g.Model = name
	g.mu.Unlock()
}

// CurrentModel implements ModelSwitch.
func (g *GrokOAuth) CurrentModel() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.Model
}

// Complete implements Client. A 401 refreshes the OAuth token once.
func (g *GrokOAuth) Complete(ctx context.Context, messages []Message, tools []ToolSpec, onDelta func(string)) (Response, error) {
	tok, err := g.Source.Token(ctx, false)
	if err != nil {
		return Response{}, err
	}
	resp, err := g.once(ctx, tok.AccessToken, messages, tools, onDelta)
	if err == nil || !isUnauthorized(err) {
		return resp, err
	}
	tok, err = g.Source.Token(ctx, true)
	if err != nil {
		return Response{}, err
	}
	return g.once(ctx, tok.AccessToken, messages, tools, onDelta)
}

func (g *GrokOAuth) once(ctx context.Context, access string, messages []Message, tools []ToolSpec, onDelta func(string)) (Response, error) {
	inner := NewOpenAI(g.Base, access, g.CurrentModel(), g.HTTP)
	return inner.Complete(ctx, messages, tools, onDelta)
}

func isUnauthorized(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "returned 401")
}
