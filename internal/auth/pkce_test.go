package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestPKCEChallengeMatchesVerifier(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Verifier) < 43 || len(p.Verifier) > 128 {
		t.Fatalf("verifier length %d", len(p.Verifier))
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if p.Challenge != want {
		t.Fatalf("challenge %s", p.Challenge)
	}
	state, err := NewState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 43 {
		t.Fatalf("state length %d", len(state))
	}
}
