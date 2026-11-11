package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestAccountIDFromChatGPTJWT(t *testing.T) {
	payload := map[string]any{
		"email": "ada@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_abc",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	token := "aaa." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
	account, email := identityFromJWTs(token)
	if account != "acct_abc" || email != "ada@example.com" {
		t.Fatalf("account %q email %q", account, email)
	}
}
