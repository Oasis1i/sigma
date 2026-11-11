package auth

import (
	"net/url"
	"os"
)

// Public client identifiers. These are the values the official CLIs ship in
// their own source (Codex, Claude Code, Gemini CLI, Grok CLI). Sigma uses
// them so a consumer subscription can sign in. They are not secrets.
// Endpoint behavior was checked against those clients and against the MIT
// CLIProxyAPI project. It was not copied from sub2api. See NOTICE.
const (
	ChatGPTAuthURL      = "https://auth.openai.com/oauth/authorize"
	ChatGPTTokenURL     = "https://auth.openai.com/oauth/token"
	ChatGPTClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	ChatGPTRedirect     = "http://localhost:1455/auth/callback"
	ChatGPTResponses    = "https://chatgpt.com/backend-api/codex/responses"
	ChatGPTScope        = "openid email profile offline_access"
	ChatGPTRefreshScope = "openid profile email"

	ClaudeAuthURL      = "https://claude.ai/oauth/authorize"
	ClaudeTokenURL     = "https://platform.claude.com/v1/oauth/token"
	ClaudeClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	ClaudeRedirect     = "http://localhost:54545/callback"
	ClaudeMessages     = "https://api.anthropic.com/v1/messages"
	ClaudeAuthScope    = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	ClaudeRefreshScope = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"

	// Gemini's installed-app client id and secret are the ones published by
	// google-gemini/gemini-cli. GitHub secret scanning rejects those values
	// in a git push, so they are not stored in this repo. Set
	// SIGMA_GEMINI_CLIENT_ID and SIGMA_GEMINI_CLIENT_SECRET from that project.
	GeminiAuthURL    = "https://accounts.google.com/o/oauth2/v2/auth"
	GeminiTokenURL   = "https://oauth2.googleapis.com/token"
	GeminiScope      = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"
	GeminiCodeAssist = "https://cloudcode-pa.googleapis.com/v1internal"

	GrokIssuer       = "https://auth.x.ai"
	GrokDiscovery    = "https://auth.x.ai/.well-known/openid-configuration"
	GrokClientID     = "b1a00492-073a-47ea-816f-4c329264a828"
	GrokScope        = "openid profile email offline_access grok-cli:access api:access"
	GrokAPIBase      = "https://api.x.ai/v1"
	GrokCLIProxyBase = "https://cli-chat-proxy.grok.com/v1"
)

// RiskWarning is printed before a borrowed-client login or request.
// ChatGPT's Codex login does not use it.
const RiskWarning = "警告：用第三方客户端调用该订阅可能违反服务商条款，账号有被封禁的风险。"

// NeedsRisk reports providers whose login reuses another product's client id
// and therefore require an explicit opt-in. chatgpt does not.
func NeedsRisk(provider string) bool {
	switch provider {
	case "claude", "gemini", "grok":
		return true
	default:
		return false
	}
}

// Known lists providers the login command understands. Inference support can
// lag this list by one pull request.
func Known() []string {
	return []string{"chatgpt", "claude", "gemini", "grok"}
}

// Spec is one provider's OAuth endpoints. TokenURL may be replaced in tests.
type Spec struct {
	Name         string
	AuthURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scope        string
	RefreshScope string
	ExtraAuth    url.Values
	// JSONToken posts the token request as JSON. Claude does. The others use
	// application/x-www-form-urlencoded.
	JSONToken bool
	// SendState includes the state value in the token POST. Claude does.
	SendState bool
}

// ChatGPTSpec is the Codex "Sign in with ChatGPT" public client.
func ChatGPTSpec() Spec {
	extra := url.Values{}
	extra.Set("prompt", "login")
	extra.Set("id_token_add_organizations", "true")
	extra.Set("codex_cli_simplified_flow", "true")
	return Spec{
		Name:         "chatgpt",
		AuthURL:      ChatGPTAuthURL,
		TokenURL:     ChatGPTTokenURL,
		ClientID:     ChatGPTClientID,
		RedirectURI:  ChatGPTRedirect,
		Scope:        ChatGPTScope,
		RefreshScope: ChatGPTRefreshScope,
		ExtraAuth:    extra,
	}
}

// ClaudeSpec is the Claude Code public client. Login is refused until the
// user opts in. See NeedsRisk.
func ClaudeSpec() Spec {
	return Spec{
		Name:         "claude",
		AuthURL:      ClaudeAuthURL,
		TokenURL:     ClaudeTokenURL,
		ClientID:     ClaudeClientID,
		RedirectURI:  ClaudeRedirect,
		Scope:        ClaudeAuthScope,
		RefreshScope: ClaudeRefreshScope,
		JSONToken:    true,
		SendState:    true,
	}
}

// GeminiSpec is the Gemini CLI installed-app client. RedirectURI is filled
// in at login time because Google loopback clients accept the chosen port.
func GeminiSpec() Spec {
	extra := url.Values{}
	extra.Set("access_type", "offline")
	extra.Set("prompt", "consent")
	return Spec{
		Name:         "gemini",
		AuthURL:      GeminiAuthURL,
		TokenURL:     GeminiTokenURL,
		ClientID:     os.Getenv("SIGMA_GEMINI_CLIENT_ID"),
		ClientSecret: os.Getenv("SIGMA_GEMINI_CLIENT_SECRET"),
		Scope:        GeminiScope,
		ExtraAuth:    extra,
	}
}

// GrokSpec is the Grok CLI public client. AuthURL and TokenURL come from
// OIDC discovery when they are empty. The caller may set them for tests.
func GrokSpec() Spec {
	extra := url.Values{}
	extra.Set("plan", "generic")
	return Spec{
		Name:      "grok",
		ClientID:  GrokClientID,
		Scope:     GrokScope,
		ExtraAuth: extra,
	}
}

// Lookup returns the built-in spec for a provider name.
func Lookup(name string) (Spec, bool) {
	switch name {
	case "chatgpt":
		return ChatGPTSpec(), true
	case "claude":
		return ClaudeSpec(), true
	case "gemini":
		return GeminiSpec(), true
	case "grok":
		return GrokSpec(), true
	default:
		return Spec{}, false
	}
}
