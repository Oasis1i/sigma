package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Oasis1i/sigma/internal/agent"
	"github.com/Oasis1i/sigma/internal/approval"
)

const slashHelp = `REPL commands:
  /help              show this help
  /mode plan|ask|auto
                     change the approval mode
  /session           print the session id
  /exit, /quit       leave the REPL
Ctrl-D also exits. Ctrl-C interrupts the current turn.
`

func repl(ag *agent.Agent, policy *approval.Policy, in *bufio.Reader, signals *sigLoop) error {
	fmt.Fprintln(os.Stderr, `Type a task, or /help. Ctrl-D exits.`)
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
		if strings.HasPrefix(line, "/") {
			exit, serr := slash(line, ag, policy)
			if serr != nil {
				fmt.Fprintf(os.Stderr, "sigma: %v\n", serr)
			}
			if exit {
				return nil
			}
			continue
		}
		err = ag.RunTurn(signals.arm(), line)
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "interrupted")
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sigma: %v\n", err)
		}
	}
}

func slash(line string, ag *agent.Agent, policy *approval.Policy) (bool, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false, nil
	}
	switch fields[0] {
	case "/exit", "/quit":
		return true, nil
	case "/help":
		fmt.Fprint(os.Stderr, slashHelp)
		return false, nil
	case "/session":
		if ag.Session == nil {
			fmt.Fprintln(os.Stderr, "no session")
			return false, nil
		}
		fmt.Fprintln(os.Stderr, ag.Session.ID)
		return false, nil
	case "/mode":
		if len(fields) != 2 {
			return false, fmt.Errorf("usage: /mode plan|ask|auto")
		}
		if err := policy.SetMode(fields[1]); err != nil {
			return false, err
		}
		fmt.Fprintf(os.Stderr, "approval mode: %s\n", fields[1])
		return false, ag.Note("Approval mode is now " + fields[1] + ".")
	default:
		return false, fmt.Errorf("unknown command %s (try /help)", fields[0])
	}
}
