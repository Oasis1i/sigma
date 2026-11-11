// Command sigma is a small coding agent. It loads a config, talks to an
// OpenAI-compatible model, and runs tools in the workspace after approval.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Oasis1i/sigma/internal/agent"
	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/config"
	"github.com/Oasis1i/sigma/internal/prompt"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/sandbox"
	"github.com/Oasis1i/sigma/internal/session"
	"github.com/Oasis1i/sigma/internal/tools"
)

const version = "0.1.0"

const usageText = `sigma is a minimal coding agent.

Usage:
  sigma                         interactive REPL
  sigma -p "task"               run one task and exit
  sigma --resume <id>           resume a session (id or unique prefix)
  sigma --continue              resume the most recent session

The model and base URL have no default. Set them in
~/.config/sigma/config.toml or .sigma.toml, or with SIGMA_MODEL,
SIGMA_BASE_URL, SIGMA_API_KEY, or the flags below. Flags win over the
environment, which wins over the workspace file, which wins over the
user file.

Approval modes:
  plan   read-only tools only
  ask    prompt before edit_file, write_file, and bash (the default)
  auto   run every call the rules allow

Rules in the config file are checked deny, then ask, then allow.
A matching deny beats an allow written above it.

Flags:
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "sigma: interrupted")
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "sigma: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("sigma", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usageText)
		fs.SetOutput(os.Stderr)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
	}

	var (
		promptStr    string
		mode         string
		model        string
		baseURL      string
		apiKey       string
		configPath   string
		workspace    string
		maxSteps     int
		resumeID     string
		continueSess bool
		showVersion  bool
	)
	fs.StringVar(&promptStr, "p", "", "one-shot task; sigma exits when the model finishes")
	fs.StringVar(&mode, "mode", "", "approval mode: plan, ask, or auto")
	fs.StringVar(&model, "model", "", "model name")
	fs.StringVar(&baseURL, "base-url", "", "OpenAI-compatible API root, for example https://api.openai.com/v1")
	fs.StringVar(&apiKey, "api-key", "", "API key (prefer SIGMA_API_KEY so it is not in the process list)")
	fs.StringVar(&configPath, "config", "", "config file to load instead of the user and workspace files")
	fs.StringVar(&workspace, "workspace", "", "workspace directory (default: current directory)")
	fs.IntVar(&maxSteps, "max-steps", 0, "maximum model calls per task (default 50)")
	fs.StringVar(&resumeID, "resume", "", "resume a session by id or unique prefix")
	fs.BoolVar(&continueSess, "continue", false, "resume the most recent session")
	fs.BoolVar(&showVersion, "version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if showVersion {
		fmt.Println("sigma", version)
		return nil
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (put the task in -p)", fs.Arg(0))
	}
	if continueSess && resumeID != "" {
		return fmt.Errorf("use either --continue or --resume, not both")
	}

	ov := config.Overrides{ConfigPath: configPath, Workspace: workspace}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "model":
			ov.ModelSet = true
			ov.Model = model
		case "base-url":
			ov.BaseURLSet = true
			ov.BaseURL = baseURL
		case "api-key":
			ov.APIKeySet = true
			ov.APIKey = apiKey
		case "mode":
			ov.ModeSet = true
			ov.Mode = mode
		case "max-steps":
			ov.MaxStepsSet = true
			ov.MaxSteps = maxSteps
		}
	})

	cfg, err := config.Load(ov)
	if err != nil {
		return err
	}
	rules := make([]approval.Rule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		rules = append(rules, approval.Rule{Action: approval.Action(strings.TrimSpace(r.Action)), Pattern: r.Pattern})
	}
	policy, err := approval.New(cfg.Approval, rules)
	if err != nil {
		return err
	}

	dir, err := session.DefaultDir()
	if err != nil {
		return err
	}
	var sess *session.Session
	resumed := continueSess || resumeID != ""
	if continueSess {
		sess, err = session.OpenLatest(dir)
	} else if resumeID != "" {
		sess, err = session.Open(dir, resumeID)
	} else {
		sess, err = session.Create(dir, map[string]string{
			"model":     cfg.Model,
			"base_url":  cfg.BaseURL,
			"mode":      cfg.Approval,
			"workspace": cfg.Workspace,
		})
	}
	if err != nil {
		return err
	}
	defer sess.Close()

	var messages []provider.Message
	if resumed {
		messages, err = sess.Messages()
		if err != nil {
			return err
		}
		remembered, err := sess.RememberedTools()
		if err != nil {
			return err
		}
		for _, tool := range remembered {
			policy.RememberAlways(tool)
		}
		fmt.Fprintf(os.Stderr, "sigma: resumed %s (%d messages)\n", sess.ID, len(messages))
	}
	if len(messages) == 0 {
		sections, err := prompt.LoadAgents(cfg.Workspace)
		if err != nil {
			return err
		}
		sys := prompt.Build(cfg.Workspace, cfg.Approval, sections)
		messages = []provider.Message{{Role: "system", Content: sys}}
		if err := sess.AppendMessage(messages[0]); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "sigma: %s @ %s\n", cfg.Model, cfg.BaseURL)
	if !resumed {
		fmt.Fprintf(os.Stderr, "sigma: session %s\n", sess.ID)
	}
	fmt.Fprintf(os.Stderr, "sigma: mode %s, workspace %s\n", policy.Mode, cfg.Workspace)

	reg := tools.NewRegistry(&tools.Env{
		Workspace:   cfg.Workspace,
		Runner:      &sandbox.Local{Dir: cfg.Workspace},
		BashTimeout: time.Duration(cfg.BashTimeoutSec) * time.Second,
	})
	in := bufio.NewReader(os.Stdin)
	ag := &agent.Agent{
		Client:   provider.NewOpenAI(cfg.BaseURL, cfg.APIKey, cfg.Model, nil),
		Registry: reg,
		Policy:   policy,
		Session:  sess,
		Messages: messages,
		MaxSteps: cfg.MaxSteps,
		Ask:      makeAsk(in),
		Out:      os.Stdout,
		Err:      os.Stderr,
	}

	signals := newSigLoop()
	if strings.TrimSpace(promptStr) != "" {
		return ag.RunTurn(signals.arm(), promptStr)
	}
	return repl(ag, policy, in, signals)
}

// sigLoop cancels the current turn on Ctrl-C. A second Ctrl-C exits.
type sigLoop struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	hits   atomic.Int32
}

func newSigLoop() *sigLoop {
	s := &sigLoop{}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		for range ch {
			if s.hits.Add(1) >= 2 {
				fmt.Fprintln(os.Stderr, "\nsigma: exiting")
				os.Exit(130)
			}
			fmt.Fprintln(os.Stderr, "\nsigma: interrupting (ctrl-c again to exit)")
			s.stop()
		}
	}()
	return s
}

func (s *sigLoop) arm() context.Context {
	s.hits.Store(0)
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	return ctx
}

func (s *sigLoop) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func makeAsk(in *bufio.Reader) agent.AskFunc {
	return func(toolName, arg string) (bool, bool, error) {
		if !isTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintf(os.Stderr, "sigma: denied %s (ask mode needs a terminal; pass --mode auto to run unattended)\n", toolName)
			return false, false, nil
		}
		fmt.Fprintf(os.Stderr, "\nAllow %s?\n  %s\n[y]es / [n]o / [a]lways > ", toolName, clip(arg, 200))
		line, err := in.ReadString('\n')
		if err != nil {
			return false, false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, false, nil
		case "a", "always":
			return true, true, nil
		default:
			return false, false, nil
		}
	}
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
