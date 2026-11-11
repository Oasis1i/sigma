package provider

import (
	"context"
	"net/http"

	"github.com/Oasis1i/sigma/internal/auth"
)

// doAuthed calls do with a stored token. A 401 refreshes once and retries.
// The caller closes the returned body.
func doAuthed(ctx context.Context, src *auth.Source, do func(auth.Token) (*http.Response, error)) (*http.Response, auth.Token, error) {
	if src == nil {
		return nil, auth.Token{}, errNoToken
	}
	tok, err := src.Token(ctx, false)
	if err != nil {
		return nil, auth.Token{}, err
	}
	resp, err := do(tok)
	if err != nil {
		return nil, tok, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, tok, nil
	}
	resp.Body.Close()
	tok, err = src.Token(ctx, true)
	if err != nil {
		return nil, tok, err
	}
	resp, err = do(tok)
	return resp, tok, err
}
