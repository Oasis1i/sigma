package auth

import (
	"os"
	"testing"
	"time"
)

func TestStorePermissionsAndStatusOmitsTokens(t *testing.T) {
	dir := t.TempDir()
	st := Store{Dir: dir + "/auth"}
	tok := Token{
		Provider:     "chatgpt",
		AccessToken:  "access-secret-value",
		RefreshToken: "refresh-secret-value",
		Expiry:       time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		AccountID:    "acct_1",
		Email:        "a@example.com",
	}
	if err := st.Save(tok); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(st.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %o", info.Mode().Perm())
	}
	path := st.Dir + "/chatgpt.json"
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %o", info.Mode().Perm())
	}
	got, err := st.Load("chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != tok.AccessToken || got.AccountID != "acct_1" {
		t.Fatalf("%+v", got)
	}
	status, err := st.Status("chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	if !status.LoggedIn || status.Email != "a@example.com" || status.AccountID != "acct_1" {
		t.Fatalf("%+v", status)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The file holds the secrets. The status value must not.
	text := status.Email + status.AccountID + status.Provider
	if text == "" || containsAny(text, "access-secret-value", "refresh-secret-value") {
		t.Fatal("status leaked a token")
	}
	if !containsAny(string(raw), "access-secret-value") {
		t.Fatal("file should contain the access token")
	}
	if err := st.Delete("chatgpt"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load("chatgpt"); !os.IsNotExist(err) {
		t.Fatalf("after delete: %v", err)
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if len(p) > 0 && len(s) >= len(p) && (s == p || len(s) > 0 && contains(s, p)) {
			return true
		}
	}
	return false
}

func contains(s, part string) bool {
	return len(part) > 0 && (len(s) >= len(part) && (s == part || indexOf(s, part) >= 0))
}

func indexOf(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
