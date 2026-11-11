package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oasis1i/sigma/internal/auth"
	"github.com/Oasis1i/sigma/internal/config"
	"github.com/Oasis1i/sigma/internal/provider"
)

func TestGrokAPIKeyDoesNotNeedOptIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	client, err := buildClient(config.Config{Provider: "grok", Model: "grok-2", APIKey: "xai-test"})
	if err != nil {
		t.Fatal(err)
	}
	oa, ok := client.(*provider.OpenAI)
	if !ok || oa.BaseURL != auth.GrokAPIBase || oa.CurrentModel() != "grok-2" {
		t.Fatalf("client %T base %v", client, client)
	}
}

func TestGrokOAuthNeedsOptIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	store := auth.Store{Dir: filepath.Join(home, "sigma", "auth")}
	if err := store.Save(auth.Token{Provider: "grok", AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_, err := buildClient(config.Config{Provider: "grok", Model: "grok-2"})
	if err == nil || !strings.Contains(err.Error(), "allow_subscription_risk") {
		t.Fatalf("got %v", err)
	}
}

func TestClaudeUseNeedsOptIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := buildClient(config.Config{Provider: "claude", Model: "claude-sonnet"})
	if err == nil || !strings.Contains(err.Error(), "警告") {
		t.Fatalf("got %v", err)
	}
}
