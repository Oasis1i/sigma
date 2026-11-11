// Command sigma is a small coding agent. It loads a config, talks to an
// OpenAI-compatible model, and runs tools in the workspace after approval.
//
// The interactive default is the TUI when stdout is a terminal. --plain
// selects the line REPL. Both, and the -p one-shot path, talk to the same
// engine: they send commands and read events. The engine never reads stdin.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Oasis1i/sigma/internal/agent"
	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/checkpoint"
	"github.com/Oasis1i/sigma/internal/config"
	"github.com/Oasis1i/sigma/internal/engine"
	"github.com/Oasis1i/sigma/internal/prompt"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/sandbox"
	"github.com/Oasis1i/sigma/internal/session"
	"github.com/Oasis1i/sigma/internal/tools"
	"github.com/Oasis1i/sigma/internal/tui"
	"golang.org/x/term"
)

const version = "0.2.0"

const usageText = `sigma is a minimal coding agent.

Usage:
  sigma                         TUI when stdout is a terminal, otherwise the plain REPL
  sigma tui                     Bubble Tea TUI
  sigma gui                     local web UI on 127.0.0.1
  sigma --plain                 line REPL, even on a terminal
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
	cmd, args := splitCommand(args)
	switch cmd {
	case "version":
		fmt.Println("sigma", version)
		return nil
	case "help":
		args = append([]string{"-h"}, args...)
		cmd = ""
	case "", "tui", "gui":
	default:
		return fmt.Errorf("unknown command %q (try sigma --help)", cmd)
	}

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
		plain        bool
		port         int
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
	fs.BoolVar(&plain, "plain", false, "line REPL instead of the TUI")
	fs.IntVar(&port, "port", 0, "sigma gui listen port; 0 picks a free port on 127.0.0.1")

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
	if cmd == "tui" && plain {
		return fmt.Errorf("tui and --plain cannot be combined")
	}
	if cmd == "tui" && strings.TrimSpace(promptStr) != "" {
		return fmt.Errorf("-p cannot be combined with tui")
	}
	if cmd == "gui" && (plain || strings.TrimSpace(promptStr) != "") {
		return fmt.Errorf("gui cannot be combined with --plain or -p")
	}
	portSet := false
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
		case "port":
			portSet = true
		}
	})
	if portSet && cmd != "gui" {
		return fmt.Errorf("--port is only valid with sigma gui")
	}

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

	var messages []provider.Message
	if resumed {
		messages, err = sess.Messages()
		if err != nil {
			sess.Close()
			return err
		}
		remembered, err := sess.RememberedTools()
		if err != nil {
			sess.Close()
			return err
		}
		for _, tool := range remembered {
			policy.RememberAlways(tool)
		}
	}
	if len(messages) == 0 {
		sections, err := prompt.LoadAgents(cfg.Workspace)
		if err != nil {
			sess.Close()
			return err
		}
		sys := prompt.Build(cfg.Workspace, cfg.Approval, sections, cfg.Verify)
		messages = []provider.Message{{Role: "system", Content: sys}}
		if err := sess.AppendMessage(messages[0]); err != nil {
			sess.Close()
			return err
		}
	}

	local := &sandbox.Local{Dir: cfg.Workspace}
	runner, err := newRunner(cfg, local)
	if err != nil {
		sess.Close()
		return err
	}
	cp := &checkpoint.Store{Dir: cfg.Workspace, Session: sess.ID}
	eng := engine.New(&agent.Agent{
		Client: provider.NewOpenAI(cfg.BaseURL, cfg.APIKey, cfg.Model, nil),
		Registry: tools.NewRegistry(&tools.Env{
			Workspace:   cfg.Workspace,
			Runner:      runner,
			BashTimeout: time.Duration(cfg.BashTimeoutSec) * time.Second,
		}),
		Policy:           policy,
		Session:          sess,
		Messages:         messages,
		MaxSteps:         cfg.MaxSteps,
		CompactThreshold: cfg.CompactThreshold,
		Verify:           cfg.Verify,
		VerifyRunner:     local,
		VerifyTimeout:    time.Duration(cfg.BashTimeoutSec) * time.Second,
	}, engine.Options{
		SessionDir:  dir,
		Model:       cfg.Model,
		BaseURL:     cfg.BaseURL,
		Workspace:   cfg.Workspace,
		Verify:      cfg.Verify,
		Checkpoints: cp,
	})
	defer eng.Close()

	if cmd == "gui" {
		return runGUI(eng, port)
	}
	useTUI := cmd == "tui" || (cmd == "" && !plain && strings.TrimSpace(promptStr) == "" && term.IsTerminal(int(os.Stdout.Fd())))
	if useTUI && !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("the TUI needs a terminal; use --plain otherwise")
	}
	if !useTUI {
		fmt.Fprintf(os.Stderr, "sigma: %s @ %s\n", cfg.Model, cfg.BaseURL)
		if resumed {
			fmt.Fprintf(os.Stderr, "sigma: resumed %s (%d messages)\n", sess.ID, len(messages))
		} else {
			fmt.Fprintf(os.Stderr, "sigma: session %s\n", sess.ID)
		}
		fmt.Fprintf(os.Stderr, "sigma: mode %s, workspace %s\n", policy.Current(), cfg.Workspace)
		return runPlain(eng, promptStr, newSigLoop(eng))
	}
	return tui.Run(eng, eng.Events(), version)
}

// splitCommand pulls a leading subcommand off the args. Flags stay in place
// so `sigma --plain` and `sigma tui --mode ask` both parse.
func splitCommand(args []string) (string, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", args
	}
	switch args[0] {
	case "tui", "help", "version":
		return args[0], args[1:]
	default:
		return args[0], args[1:]
	}
}

// sigLoop cancels the current turn on Ctrl-C. A second Ctrl-C exits.
// It is installed for the plain REPL only. The TUI is in raw mode, so
// ctrl+c arrives as a key and the model handles it.
type sigLoop struct {
	hits atomic.Int32
	eng  *engine.Engine
}

func newSigLoop(eng *engine.Engine) *sigLoop {
	s := &sigLoop{eng: eng}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		for range ch {
			if s.hits.Add(1) >= 2 {
				fmt.Fprintln(os.Stderr, "\nsigma: exiting")
				os.Exit(130)
			}
			fmt.Fprintln(os.Stderr, "\nsigma: interrupting (ctrl-c again to exit)")
			eng.Cancel()
		}
	}()
	return s
}

func (s *sigLoop) beginTurn() {
	if s != nil {
		s.hits.Store(0)
	}
}

// newRunner picks where bash runs. local is always the verify runner; docker
// replaces only the model's bash tool. Approval is unchanged either way.
func newRunner(cfg config.Config, local *sandbox.Local) (sandbox.Runner, error) {
	switch cfg.Sandbox {
	case "", "local":
		return local, nil
	case "docker":
		return &sandbox.Docker{Image: cfg.DockerImage, Dir: cfg.Workspace}, nil
	default:
		return nil, fmt.Errorf("unknown sandbox %q", cfg.Sandbox)
	}
}
