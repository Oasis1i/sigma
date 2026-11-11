package main

import (
	"strings"
	"testing"
)

func TestGeminiLoginNeedsClientEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SIGMA_GEMINI_CLIENT_ID", "")
	t.Setenv("SIGMA_GEMINI_CLIENT_SECRET", "")
	err := loginCommand([]string{"--workspace", t.TempDir(), "gemini"})
	if err == nil || !strings.Contains(err.Error(), "SIGMA_GEMINI_CLIENT_ID") {
		t.Fatalf("got %v", err)
	}
}

func TestClaudeLoginRefusesWithoutOptIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	err := loginCommand([]string{"--workspace", t.TempDir(), "claude"})
	if err == nil || !strings.Contains(err.Error(), "allow_subscription_risk") {
		t.Fatalf("got %v", err)
	}
}
