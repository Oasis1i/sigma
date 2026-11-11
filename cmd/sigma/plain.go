package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Oasis1i/sigma/internal/engine"
	"github.com/Oasis1i/sigma/internal/headless"
	"golang.org/x/term"
)

// runPlain drives the line-oriented REPL and the -p one-shot path. Both are
// clients of the engine: assistant text is an event, and an approval prompt
// is answered with a command. This file is the only place that reads stdin
// for those answers.
func runPlain(eng *engine.Engine, oneShot string, signals *sigLoop, output string) error {
	in := bufio.NewReader(os.Stdin)
	if strings.TrimSpace(oneShot) != "" {
		signals.beginTurn()
		eng.Submit(engine.Command{Kind: engine.CmdMessage, Text: oneShot})
		return consumeTurn(eng, in, output)
	}
	if output != "" && output != "text" {
		return fmt.Errorf("--output %s is only valid with -p", output)
	}
	fmt.Fprintln(os.Stderr, "Type a task, or /help. Ctrl-D exits. Ctrl-C interrupts the current turn.")
	for {
		fmt.Fprint(os.Stderr, "sigma> ")
		line, err := in.ReadString('\n')
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(os.Stderr)
			return nil
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") && !strings.Contains(line, "\n") {
			effect, serr := eng.ApplySlash(line)
			if serr != nil {
				fmt.Fprintf(os.Stderr, "sigma: %v\n", serr)
				continue
			}
			if effect.Notice != "" {
				fmt.Fprintln(os.Stderr, strings.TrimRight(effect.Notice, "\n"))
			}
			if effect.Quit {
				return nil
			}
			continue
		}
		signals.beginTurn()
		eng.Submit(engine.Command{Kind: engine.CmdMessage, Text: line})
		err = consumeTurn(eng, in, "text")
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "interrupted")
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sigma: %v\n", err)
		}
	}
}

// consumeTurn reads events until the turn is done. Assistant text goes to
// stdout. Steps, tools, and notices go to stderr, so a one-shot redirect
// keeps the reply.
func consumeTurn(eng *engine.Engine, in *bufio.Reader, output string) error {
	var lineOpen bool
	var turnErr error
	var report headless.Report
	var recorded []engine.Event
	jsonl := output == "jsonl"
	collectJSON := output == "json"
	for {
		ev, ok := <-eng.Events()
		if !ok {
			return fmt.Errorf("event stream closed")
		}
		report.Add(ev)
		if collectJSON {
			recorded = append(recorded, ev)
		}
		if jsonl {
			if err := headless.WriteJSONL(os.Stdout, ev); err != nil {
				return err
			}
		}
		if !jsonl && !collectJSON {
			if err := printPlain(ev, &lineOpen, in, eng); err != nil {
				return err
			}
		} else if ev.Kind == engine.KindApproval {
			allow, always := askOnTerminal(in, ev)
			eng.Submit(engine.Command{
				Kind:       engine.CmdApproval,
				ApprovalID: ev.ApprovalID,
				Allow:      allow,
				Always:     always,
			})
		}
		if ev.Kind == engine.KindError && turnErr == nil {
			turnErr = errors.New(ev.Text)
		}
		if ev.Kind == engine.KindDone {
			if collectJSON {
				report.Events = recorded
				if err := headless.WriteJSON(os.Stdout, report); err != nil {
					return err
				}
			}
			if ev.Canceled {
				return context.Canceled
			}
			return turnErr
		}
	}
}

func printPlain(ev engine.Event, lineOpen *bool, in *bufio.Reader, eng *engine.Engine) error {
	switch ev.Kind {
	case engine.KindTextDelta:
		fmt.Fprint(os.Stdout, ev.Text)
		*lineOpen = !strings.HasSuffix(ev.Text, "\n")
	case engine.KindStep:
		finishLine(lineOpen)
		fmt.Fprintf(os.Stderr, "· step %d/%d\n", ev.Step, ev.MaxSteps)
	case engine.KindToolStart:
		finishLine(lineOpen)
		shown := ev.Preview
		if shown == "" {
			shown = ev.Args
		}
		fmt.Fprintf(os.Stderr, "· %s %s\n", ev.Tool, clip(shown, 120))
	case engine.KindToolEnd:
		if ev.Failed {
			finishLine(lineOpen)
			fmt.Fprintf(os.Stderr, "· %s error: %s\n", ev.Tool, clip(ev.Output, 200))
		}
	case engine.KindNotice:
		finishLine(lineOpen)
		fmt.Fprintf(os.Stderr, "· %s\n", ev.Text)
	case engine.KindUsage:
		finishLine(lineOpen)
		fmt.Fprintf(os.Stderr, "· tokens %d (prompt %d, completion %d)\n", ev.TotalTokens, ev.PromptTokens, ev.CompletionTokens)
	case engine.KindSubagent:
		finishLine(lineOpen)
		if ev.Phase == "step" && ev.Step > 0 {
			fmt.Fprintf(os.Stderr, "· explore %s step %d/%d %s\n", ev.SubagentID, ev.Step, ev.MaxSteps, clip(ev.Text, 80))
		} else {
			fmt.Fprintf(os.Stderr, "· explore %s %s %s\n", ev.SubagentID, ev.Phase, clip(ev.Text, 120))
		}
	case engine.KindTodo:
		finishLine(lineOpen)
		fmt.Fprintf(os.Stderr, "· todos updated (%d)\n", len(ev.Todos))
	case engine.KindApproval:
		finishLine(lineOpen)
		allow, always := askOnTerminal(in, ev)
		eng.Submit(engine.Command{
			Kind:       engine.CmdApproval,
			ApprovalID: ev.ApprovalID,
			Allow:      allow,
			Always:     always,
		})
	case engine.KindError:
		finishLine(lineOpen)
	case engine.KindDone:
		finishLine(lineOpen)
	}
	return nil
}

func askOnTerminal(in *bufio.Reader, ev engine.Event) (allow, always bool) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "sigma: denied %s (ask mode needs a terminal; pass --mode auto to run unattended)\n", ev.Tool)
		return false, false
	}
	fmt.Fprintf(os.Stderr, "\nAllow %s?\n  %s\n[y]es / [n]o / [a]lways > ", ev.Tool, clip(ev.Args, 200))
	line, err := in.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "sigma: denied %s (%v)\n", ev.Tool, err)
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, false
	case "a", "always":
		return true, true
	default:
		return false, false
	}
}

func finishLine(open *bool) {
	if *open {
		fmt.Fprintln(os.Stdout)
		*open = false
	}
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
