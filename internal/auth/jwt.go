package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

var errNotJWT = errors.New("not a jwt")

// AccountFromToken reads chatgpt_account_id and email from an access or id
// token. The signature is not checked. The values only label a credential
// this process already holds.
func AccountFromToken(token string) (accountID, email string) {
	return identityFromJWTs(token)
}

// identityFromJWTs reads the account id and email out of tokens the token
// endpoint just returned. The signature is not checked: these values only
// label a credential we already hold, they are not used to authorize anyone.
func identityFromJWTs(tokens ...string) (accountID, email string) {
	for _, raw := range tokens {
		claims, err := jwtPayload(raw)
		if err != nil {
			continue
		}
		if email == "" {
			email = stringClaim(claims, "email")
		}
		if accountID == "" {
			accountID = accountFromClaims(claims)
		}
	}
	return accountID, email
}

func jwtPayload(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, errNotJWT
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		body, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, err
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func accountFromClaims(claims map[string]any) string {
	if id := stringClaim(claims, "chatgpt_account_id"); id != "" {
		return id
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	if id := stringClaim(auth, "chatgpt_account_id"); id != "" {
		return id
	}
	return ""
}

func stringClaim(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}
	s, _ := claims[key].(string)
	return strings.TrimSpace(s)
}
