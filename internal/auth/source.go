package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// Source loads a stored token and refreshes it shortly before expiry, or
// when Force is true after a 401. Refresh is serialized so two requests do
// not rotate the same refresh token at once.
type Source struct {
	Name  string
	Spec  Spec
	Store Store
	HTTP  *http.Client
	Skew  time.Duration
	Now   func() time.Time

	mu sync.Mutex
}

// Token returns a usable access token. force refreshes even when the stored
// token has not expired.
func (s *Source) Token(ctx context.Context, force bool) (Token, error) {
	if s.Skew <= 0 {
		s.Skew = 2 * time.Minute
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, err := s.Store.Load(s.Name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Token{}, fmt.Errorf("%s: not logged in (sigma login %s)", s.Name, s.Name)
		}
		return Token{}, err
	}
	if !force && tok.AccessToken != "" && (tok.Expiry.IsZero() || tok.Expiry.After(now().Add(s.Skew))) {
		return tok, nil
	}
	if tok.RefreshToken == "" {
		return Token{}, fmt.Errorf("%s: access token expired and no refresh token is stored; run sigma login %s", s.Name, s.Name)
	}
	next, err := Refresh(ctx, s.HTTP, s.Spec, tok.RefreshToken)
	if err != nil {
		return Token{}, err
	}
	if next.AccountID == "" {
		next.AccountID = tok.AccountID
	}
	if next.Email == "" {
		next.Email = tok.Email
	}
	if next.Extra == nil {
		next.Extra = tok.Extra
	}
	if err := s.Store.Save(next); err != nil {
		return Token{}, err
	}
	return next, nil
}
