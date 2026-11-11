// Package config loads Sigma settings from files, the environment, and flags.
//
// Later sources win for scalar fields: the user file, then .sigma.toml in the
// workspace, then SIGMA_BASE_URL / SIGMA_MODEL / SIGMA_API_KEY, then CLI flags.
// Rules from the user file and the workspace file are combined. There is no
// default model or base URL; a missing value is an error.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	DefaultMaxSteps         = 50
	DefaultBashTimeoutSec   = 120
	DefaultMode             = "ask"
	DefaultCompactThreshold = 80000
	DefaultSandbox          = "local"
	DefaultExploreSteps     = 8
	DefaultExploreTokens    = 8000
)

// MCPServer is one external tool server. Set command for stdio, or url for
// streamable HTTP. Not both.
type MCPServer struct {
	Name    string   `toml:"name"`
	Command []string `toml:"command"`
	URL     string   `toml:"url"`
	Env     []string `toml:"env"`
}
type Rule struct {
	Action  string `toml:"action"`
	Pattern string `toml:"pattern"`
}

// file is the on-disk shape. Empty scalar fields mean "not set", except
// compact_threshold: 0 is a real value (compaction off) when the key is
// present. apply uses the decode metadata to tell those apart.
type file struct {
	BaseURL          string      `toml:"base_url"`
	Model            string      `toml:"model"`
	APIKey           string      `toml:"api_key"`
	MaxSteps         int         `toml:"max_steps"`
	BashTimeout      int         `toml:"bash_timeout_seconds"`
	Approval         string      `toml:"approval_mode"`
	CompactThreshold int         `toml:"compact_threshold"`
	Sandbox          string      `toml:"sandbox"`
	DockerImage      string      `toml:"docker_image"`
	Verify           []string    `toml:"verify"`
	ExploreMaxSteps  int         `toml:"explore_max_steps"`
	ExploreMaxTokens int         `toml:"explore_max_tokens"`
	MCP              []MCPServer `toml:"mcp"`
	Rules            []Rule      `toml:"rules"`
}

// Config is the fully resolved settings for one run.
type Config struct {
	BaseURL        string
	Model          string
	APIKey         string
	MaxSteps       int
	BashTimeoutSec int
	Approval       string
	Rules          []Rule
	Workspace      string
	// CompactThreshold is an estimated token count. Above it, older turns are
	// summarized. 0 disables compaction.
	CompactThreshold int
	// Verify commands run after a successful edit_file or write_file.
	// Failures are given back to the model. Empty disables the feature.
	Verify           []string
	Sandbox          string // local or docker
	DockerImage      string
	ExploreMaxSteps  int
	ExploreMaxTokens int
	MCP              []MCPServer
}

// Overrides carries CLI flags. A Set field replaces the loaded value even
// when the flag is empty, so an explicit empty --model still fails clearly.
type Overrides struct {
	ConfigPath  string
	Workspace   string
	BaseURL     string
	Model       string
	APIKey      string
	Mode        string
	MaxSteps    int
	BaseURLSet  bool
	ModelSet    bool
	APIKeySet   bool
	ModeSet     bool
	MaxStepsSet bool
}

// Load resolves configuration. Workspace defaults to the current directory
// when ov.Workspace is empty.
func Load(ov Overrides) (Config, error) {
	workspace := ov.Workspace
	if workspace == "" {
		wd, err := os.Getwd()
		if err != nil {
			return Config{}, fmt.Errorf("get working directory: %w", err)
		}
		workspace = wd
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return Config{}, fmt.Errorf("workspace: %w", err)
	}
	workspace = filepath.Clean(abs)
	info, err := os.Stat(workspace)
	if err != nil {
		return Config{}, fmt.Errorf("workspace: %w", err)
	}
	if !info.IsDir() {
		return Config{}, fmt.Errorf("workspace %s is not a directory", workspace)
	}

	cfg := Config{
		MaxSteps:         DefaultMaxSteps,
		BashTimeoutSec:   DefaultBashTimeoutSec,
		Approval:         DefaultMode,
		Workspace:        workspace,
		CompactThreshold: DefaultCompactThreshold,
		Sandbox:          DefaultSandbox,
		ExploreMaxSteps:  DefaultExploreSteps,
		ExploreMaxTokens: DefaultExploreTokens,
	}

	if ov.ConfigPath != "" {
		f, md, found, err := readFile(ov.ConfigPath)
		if err != nil {
			return Config{}, err
		}
		if !found {
			return Config{}, fmt.Errorf("config file not found: %s", ov.ConfigPath)
		}
		apply(&cfg, f, md)
	} else {
		if path := userConfigPath(); path != "" {
			f, md, found, err := readFile(path)
			if err != nil {
				return Config{}, err
			}
			if found {
				apply(&cfg, f, md)
			}
		}
		project := filepath.Join(workspace, ".sigma.toml")
		f, md, found, err := readFile(project)
		if err != nil {
			return Config{}, err
		}
		if found {
			apply(&cfg, f, md)
		}
	}

	if v := os.Getenv("SIGMA_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("SIGMA_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("SIGMA_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if ov.BaseURLSet {
		cfg.BaseURL = ov.BaseURL
	}
	if ov.ModelSet {
		cfg.Model = ov.Model
	}
	if ov.APIKeySet {
		cfg.APIKey = ov.APIKey
	}
	if ov.ModeSet {
		cfg.Approval = ov.Mode
	}
	if ov.MaxStepsSet {
		cfg.MaxSteps = ov.MaxSteps
	}

	if err := validate(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func apply(cfg *Config, f file, md toml.MetaData) {
	if f.BaseURL != "" {
		cfg.BaseURL = f.BaseURL
	}
	if f.Model != "" {
		cfg.Model = f.Model
	}
	if f.APIKey != "" {
		cfg.APIKey = f.APIKey
	}
	if f.MaxSteps > 0 {
		cfg.MaxSteps = f.MaxSteps
	}
	if f.BashTimeout > 0 {
		cfg.BashTimeoutSec = f.BashTimeout
	}
	if f.Approval != "" {
		cfg.Approval = f.Approval
	}
	if len(f.Rules) > 0 {
		cfg.Rules = append(cfg.Rules, f.Rules...)
	}
	// 0 is meaningful (disable compaction), so absence of the key must not
	// look like an explicit zero. The same is true for an empty verify list.
	if md.IsDefined("compact_threshold") {
		cfg.CompactThreshold = f.CompactThreshold
	}
	if md.IsDefined("sandbox") && strings.TrimSpace(f.Sandbox) != "" {
		cfg.Sandbox = strings.TrimSpace(f.Sandbox)
	}
	if md.IsDefined("docker_image") && strings.TrimSpace(f.DockerImage) != "" {
		cfg.DockerImage = strings.TrimSpace(f.DockerImage)
	}
	if md.IsDefined("verify") {
		cfg.Verify = append(cfg.Verify, f.Verify...)
	}
	if f.ExploreMaxSteps > 0 {
		cfg.ExploreMaxSteps = f.ExploreMaxSteps
	}
	if f.ExploreMaxTokens > 0 {
		cfg.ExploreMaxTokens = f.ExploreMaxTokens
	}
	if md.IsDefined("mcp") {
		cfg.MCP = append(cfg.MCP, f.MCP...)
	}
}

func validate(cfg *Config) error {
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("model is not set; set it in ~/.config/sigma/config.toml or .sigma.toml, or pass SIGMA_MODEL / --model")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return fmt.Errorf("base_url is not set; set it in ~/.config/sigma/config.toml or .sigma.toml, or pass SIGMA_BASE_URL / --base-url")
	}
	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("base_url %q is not an absolute http(s) URL", cfg.BaseURL)
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Model = strings.TrimSpace(cfg.Model)
	switch cfg.Approval {
	case "plan", "ask", "auto":
	default:
		return fmt.Errorf("unknown approval mode %q (want plan, ask, or auto)", cfg.Approval)
	}
	if cfg.MaxSteps <= 0 {
		return fmt.Errorf("max_steps must be positive")
	}
	if cfg.BashTimeoutSec <= 0 {
		return fmt.Errorf("bash_timeout_seconds must be positive")
	}
	if cfg.CompactThreshold < 0 {
		return fmt.Errorf("compact_threshold must be zero or positive")
	}
	switch cfg.Sandbox {
	case "", "local":
		cfg.Sandbox = "local"
	case "docker":
		if strings.TrimSpace(cfg.DockerImage) == "" {
			return fmt.Errorf("docker_image is required when sandbox = \"docker\"")
		}
		cfg.DockerImage = strings.TrimSpace(cfg.DockerImage)
	default:
		return fmt.Errorf("unknown sandbox %q (want local or docker)", cfg.Sandbox)
	}
	for _, cmd := range cfg.Verify {
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("verify contains an empty command")
		}
	}
	for _, r := range cfg.Rules {
		switch strings.TrimSpace(r.Action) {
		case "deny", "ask", "allow":
		default:
			return fmt.Errorf("invalid rule action %q", r.Action)
		}
		if strings.TrimSpace(r.Pattern) == "" {
			return fmt.Errorf("rule action %q has an empty pattern", r.Action)
		}
	}
	seenMCP := map[string]bool{}
	for _, srv := range cfg.MCP {
		name := strings.TrimSpace(srv.Name)
		if name == "" {
			return fmt.Errorf("mcp server is missing a name")
		}
		if strings.Contains(name, "__") || strings.ContainsAny(name, " \t\n") {
			return fmt.Errorf("mcp server name %q cannot contain spaces or __", name)
		}
		if seenMCP[name] {
			return fmt.Errorf("duplicate mcp server %q", name)
		}
		seenMCP[name] = true
		hasCmd := len(srv.Command) > 0
		hasURL := strings.TrimSpace(srv.URL) != ""
		if hasCmd == hasURL {
			return fmt.Errorf("mcp server %q needs exactly one of command or url", name)
		}
	}
	return nil
}

func readFile(path string) (file, toml.MetaData, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return file{}, toml.MetaData{}, false, nil
	}
	if err != nil {
		return file{}, toml.MetaData{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	var f file
	md, err := toml.Decode(string(b), &f)
	if err != nil {
		return file{}, toml.MetaData{}, true, fmt.Errorf("%s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return file{}, md, true, fmt.Errorf("%s: unknown keys: %s", path, undecoded)
	}
	if f.MaxSteps < 0 {
		return file{}, md, true, fmt.Errorf("%s: max_steps must be positive", path)
	}
	if f.BashTimeout < 0 {
		return file{}, md, true, fmt.Errorf("%s: bash_timeout_seconds must be positive", path)
	}
	if f.CompactThreshold < 0 {
		return file{}, md, true, fmt.Errorf("%s: compact_threshold must be zero or positive", path)
	}
	if f.ExploreMaxSteps < 0 || f.ExploreMaxTokens < 0 {
		return file{}, md, true, fmt.Errorf("%s: explore limits must be positive", path)
	}
	return f, md, true, nil
}

func userConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "sigma", "config.toml")
}

// UserAgentsPath is ~/.config/sigma/AGENTS.md (or $XDG_CONFIG_HOME/sigma/AGENTS.md).
func UserAgentsPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "sigma", "AGENTS.md")
}
