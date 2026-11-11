package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCE is an OAuth proof-key pair. The verifier stays on this machine. The
// authorization URL carries only the S256 challenge.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE returns a verifier and its S256 challenge. The verifier is 32
// random bytes, base64url-encoded, which satisfies the 43–128 character
// range in RFC 7636.
func NewPKCE() (PKCE, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return PKCE{}, fmt.Errorf("pkce: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

// NewState returns a CSRF state value. 32 bytes encoded as base64url is 43
// characters, which is what Claude's authorize endpoint currently accepts.
func NewState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
