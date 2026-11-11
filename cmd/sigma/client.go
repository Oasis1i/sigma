package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Oasis1i/sigma/internal/auth"
	"github.com/Oasis1i/sigma/internal/config"
	"github.com/Oasis1i/sigma/internal/provider"
)

// buildClient picks the model client for this run. openai is the existing
// chat-completions client. chatgpt uses the stored subscription token.
// claude, gemini, and grok are recognized so the config is valid; their
// request adapters land in the next change. They still require the opt-in.
func buildClient(cfg config.Config) (provider.Client, error) {
	switch cfg.Provider {
	case "openai":
		return provider.NewOpenAI(cfg.BaseURL, cfg.APIKey, cfg.Model, nil), nil
	case "chatgpt":
		store, err := auth.DefaultStore()
		if err != nil {
			return nil, err
		}
		src := &auth.Source{
			Name:  "chatgpt",
			Spec:  auth.ChatGPTSpec(),
			Store: store,
			Skew:  2 * time.Minute,
		}
		return provider.NewChatGPT(cfg.Model, cfg.BaseURL, src, nil), nil
	case "claude", "gemini", "grok":
		if err := requireRisk(cfg); err != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, auth.RiskWarning)
		return nil, fmt.Errorf("%s is recognized, but its request adapter is not in this build yet", cfg.Provider)
	default:
		return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
	}
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
