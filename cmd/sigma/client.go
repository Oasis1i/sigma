package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Oasis1i/sigma/internal/auth"
	"github.com/Oasis1i/sigma/internal/config"
	"github.com/Oasis1i/sigma/internal/provider"
)

// buildClient picks the model client for this run.
// openai is chat completions at base_url.
// chatgpt, claude, and gemini use a stored subscription token.
// grok uses a stored OAuth token when one exists, and otherwise an xAI API
// key against https://api.x.ai/v1. The API-key path does not need the
// subscription opt-in. The OAuth path does, because it borrows the Grok CLI
// client id.
func buildClient(cfg config.Config) (provider.Client, error) {
	switch cfg.Provider {
	case "openai":
		return provider.NewOpenAI(cfg.BaseURL, cfg.APIKey, cfg.Model, nil), nil
	case "chatgpt":
		src, err := tokenSource("chatgpt", auth.ChatGPTSpec())
		if err != nil {
			return nil, err
		}
		return provider.NewChatGPT(cfg.Model, cfg.BaseURL, src, nil), nil
	case "claude":
		if err := requireRisk(cfg); err != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, auth.RiskWarning)
		src, err := tokenSource("claude", auth.ClaudeSpec())
		if err != nil {
			return nil, err
		}
		return provider.NewClaude(cfg.Model, cfg.BaseURL, src, nil), nil
	case "gemini":
		if err := requireRisk(cfg); err != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, auth.RiskWarning)
		src, err := tokenSource("gemini", auth.GeminiSpec())
		if err != nil {
			return nil, err
		}
		return provider.NewGemini(cfg.Model, cfg.BaseURL, src, nil), nil
	case "grok":
		return buildGrok(cfg)
	default:
		return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
	}
}

func buildGrok(cfg config.Config) (provider.Client, error) {
	store, err := auth.DefaultStore()
	if err != nil {
		return nil, err
	}
	tok, err := store.Load("grok")
	loggedIn := err == nil && tok.AccessToken != ""
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if loggedIn {
		if err := requireRisk(cfg); err != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, auth.RiskWarning)
		src, err := tokenSource("grok", auth.GrokSpec())
		if err != nil {
			return nil, err
		}
		return provider.NewGrokOAuth(cfg.Model, cfg.BaseURL, src, nil), nil
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("grok needs sigma login grok, or an api_key for https://api.x.ai/v1")
	}
	base := cfg.BaseURL
	if base == "" {
		base = auth.GrokAPIBase
	}
	return provider.NewOpenAI(base, cfg.APIKey, cfg.Model, nil), nil
}

// subagentClient is nil when the child should use the parent client.
func subagentClient(cfg config.Config) (provider.Client, error) {
	if cfg.SubagentProvider == "" && cfg.SubagentModel == "" && cfg.SubagentBaseURL == "" && cfg.SubagentAPIKey == "" {
		return nil, nil
	}
	next := cfg
	if cfg.SubagentProvider != "" {
		next.Provider = cfg.SubagentProvider
	}
	if cfg.SubagentModel != "" {
		next.Model = cfg.SubagentModel
	}
	if cfg.SubagentBaseURL != "" {
		next.BaseURL = cfg.SubagentBaseURL
	}
	if cfg.SubagentAPIKey != "" {
		next.APIKey = cfg.SubagentAPIKey
	}
	return buildClient(next)
}

func tokenSource(name string, spec auth.Spec) (*auth.Source, error) {
	store, err := auth.DefaultStore()
	if err != nil {
		return nil, err
	}
	return &auth.Source{Name: name, Spec: spec, Store: store, Skew: 2 * time.Minute}, nil
}

func requireRisk(cfg config.Config) error {
	if !auth.NeedsRisk(cfg.Provider) {
		return nil
	}
	if config.OptIn(cfg.AllowSubscriptionRisk, cfg.Subscription, cfg.Provider) {
		return nil
	}
	return fmt.Errorf("%s\nset allow_subscription_risk = true, or subscription.%s = true, before using %s", auth.RiskWarning, cfg.Provider, cfg.Provider)
}
