package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("SIGMA_MODEL", "")
	t.Setenv("SIGMA_BASE_URL", "")
	t.Setenv("SIGMA_API_KEY", "")
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMergeOrder(t *testing.T) {
	isolate(t)
	ws := t.TempDir()
	user := userConfigPath()
	write(t, user, `
base_url = "http://user.example/v1"
model = "user-model"
api_key = "user-key"
approval_mode = "plan"
max_steps = 10
bash_timeout_seconds = 15

[[rules]]
action = "deny"
pattern = "bash:rm *"
`)
	write(t, filepath.Join(ws, ".sigma.toml"), `
model = "project-model"
approval_mode = "ask"

[[rules]]
action = "allow"
pattern = "bash:go test*"
`)
	t.Setenv("SIGMA_MODEL", "env-model")

	cfg, err := Load(Overrides{Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "http://user.example/v1" {
		t.Fatalf("base url: %s", cfg.BaseURL)
	}
	if cfg.Model != "env-model" {
		t.Fatalf("env should override files, got %s", cfg.Model)
	}
	if cfg.APIKey != "user-key" || cfg.Approval != "ask" || cfg.MaxSteps != 10 || cfg.BashTimeoutSec != 15 {
		t.Fatalf("%+v", cfg)
	}
	if len(cfg.Rules) != 2 || cfg.Rules[0].Pattern != "bash:rm *" || cfg.Rules[1].Pattern != "bash:go test*" {
		t.Fatalf("rules: %+v", cfg.Rules)
	}

	cfg, err = Load(Overrides{Workspace: ws, Model: "flag-model", ModelSet: true, Mode: "auto", ModeSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "flag-model" || cfg.Approval != "auto" {
		t.Fatalf("flags should win, got %+v", cfg)
	}
}

func TestMissingModelAndBaseURL(t *testing.T) {
	isolate(t)
	ws := t.TempDir()
	_, err := Load(Overrides{Workspace: ws, BaseURL: "http://127.0.0.1:9/v1", BaseURLSet: true})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("got %v", err)
	}
	_, err = Load(Overrides{Workspace: ws, Model: "m", ModelSet: true})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("got %v", err)
	}
	_, err = Load(Overrides{Workspace: ws, Model: "m", ModelSet: true, BaseURL: "not a url", BaseURLSet: true})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("got %v", err)
	}
}

func TestRejectBadFile(t *testing.T) {
	isolate(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, ".sigma.toml"), `
base_url = "http://127.0.0.1:9/v1"
model = "m"
approval_mode = "sometimes"
`)
	if _, err := Load(Overrides{Workspace: ws}); err == nil {
		t.Fatal("expected bad mode")
	}
	write(t, filepath.Join(ws, ".sigma.toml"), `
base_url = "http://127.0.0.1:9/v1"
model = "m"
nope = true
`)
	if _, err := Load(Overrides{Workspace: ws}); err == nil || !strings.Contains(err.Error(), "unknown keys") {
		t.Fatalf("got %v", err)
	}
	if _, err := Load(Overrides{Workspace: ws, ConfigPath: filepath.Join(ws, "missing.toml")}); err == nil {
		t.Fatal("expected missing file")
	}
}

func TestExampleConfigParses(t *testing.T) {
	isolate(t)
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "config.example.toml")
	cfg, err := Load(Overrides{Workspace: t.TempDir(), ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model == "" || cfg.BaseURL == "" {
		t.Fatalf("%+v", cfg)
	}
	foundDeny := false
	for _, r := range cfg.Rules {
		if r.Action == "deny" && strings.Contains(r.Pattern, "rm -rf") {
			foundDeny = true
		}
	}
	if !foundDeny {
		t.Fatalf("example rules: %+v", cfg.Rules)
	}
}

func TestV2Settings(t *testing.T) {
	isolate(t)
	ws := t.TempDir()
	cfg, err := Load(Overrides{
		Workspace: ws,
		Model:     "m", ModelSet: true,
		BaseURL: "http://example.test/v1", BaseURLSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompactThreshold != DefaultCompactThreshold || cfg.Sandbox != "local" || len(cfg.Verify) != 0 {
		t.Fatalf("defaults %+v", cfg)
	}

	write(t, filepath.Join(ws, ".sigma.toml"), `
model = "m"
base_url = "http://example.test/v1"
compact_threshold = 0
sandbox = "docker"
docker_image = "golang:1.22"
verify = ["go test ./..."]
`)
	cfg, err = Load(Overrides{Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompactThreshold != 0 || cfg.Sandbox != "docker" || cfg.DockerImage != "golang:1.22" {
		t.Fatalf("%+v", cfg)
	}
	if len(cfg.Verify) != 1 || cfg.Verify[0] != "go test ./..." {
		t.Fatalf("verify %+v", cfg.Verify)
	}

	write(t, filepath.Join(ws, ".sigma.toml"), `
model = "m"
base_url = "http://example.test/v1"
sandbox = "docker"
`)
	if _, err := Load(Overrides{Workspace: ws}); err == nil {
		t.Fatal("docker without an image should fail")
	}
}
