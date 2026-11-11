// Package auth stores subscription tokens and runs the OAuth PKCE login
// those providers share. Token files are mode 0600. Nothing in this package
// writes a token to a log line or an error string.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Token is one provider's credential. Extra holds vendor-specific values
// such as a Gemini project id. Access and refresh tokens must not be printed.
type Token struct {
	Provider     string            `json:"provider"`
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	TokenType    string            `json:"token_type,omitempty"`
	Expiry       time.Time         `json:"expiry"`
	AccountID    string            `json:"account_id,omitempty"`
	Email        string            `json:"email,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// Status is safe to show in the UI. It has no tokens.
type Status struct {
	Provider  string    `json:"provider"`
	LoggedIn  bool      `json:"logged_in"`
	Email     string    `json:"email,omitempty"`
	AccountID string    `json:"account_id,omitempty"`
	Expiry    time.Time `json:"expiry,omitempty"`
}

// Store is the directory of <provider>.json files.
type Store struct {
	Dir string
}

// DefaultStore is $XDG_CONFIG_HOME/sigma/auth, or ~/.config/sigma/auth.
func DefaultStore() (Store, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return Store{}, fmt.Errorf("home directory: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return Store{Dir: filepath.Join(base, "sigma", "auth")}, nil
}

func (s Store) path(provider string) (string, error) {
	name := strings.TrimSpace(provider)
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid provider name %q", provider)
	}
	if s.Dir == "" {
		return "", fmt.Errorf("auth store directory is empty")
	}
	return filepath.Join(s.Dir, name+".json"), nil
}

// Save writes the token with mode 0600. The directory is mode 0700.
func (s Store) Save(tok Token) error {
	if strings.TrimSpace(tok.Provider) == "" {
		return fmt.Errorf("token is missing a provider")
	}
	if strings.TrimSpace(tok.AccessToken) == "" {
		return fmt.Errorf("token is missing an access token")
	}
	path, err := s.path(tok.Provider)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("auth dir: %w", err)
	}
	if err := os.Chmod(s.Dir, 0o700); err != nil {
		return fmt.Errorf("auth dir mode: %w", err)
	}
	body, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Load reads one provider. A missing file is os.ErrNotExist.
func (s Store) Load(provider string) (Token, error) {
	path, err := s.path(provider)
	if err != nil {
		return Token{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Token{}, os.ErrNotExist
		}
		return Token{}, fmt.Errorf("read token: %w", err)
	}
	var tok Token
	if err := json.Unmarshal(body, &tok); err != nil {
		return Token{}, fmt.Errorf("parse token file: %w", err)
	}
	tok.Provider = provider
	return tok, nil
}

// Delete removes a provider file. A missing file is not an error.
func (s Store) Delete(provider string) error {
	path, err := s.path(provider)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Status reports whether a provider is logged in, without the tokens.
func (s Store) Status(provider string) (Status, error) {
	tok, err := s.Load(provider)
	if errors.Is(err, os.ErrNotExist) {
		return Status{Provider: provider}, nil
	}
	if err != nil {
		return Status{}, err
	}
	return statusOf(tok), nil
}

func statusOf(tok Token) Status {
	return Status{
		Provider:  tok.Provider,
		LoggedIn:  tok.AccessToken != "",
		Email:     tok.Email,
		AccountID: tok.AccountID,
		Expiry:    tok.Expiry,
	}
}
