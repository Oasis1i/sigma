package engine

import (
	"fmt"
	"strings"

	"github.com/Oasis1i/sigma/internal/session"
)

// SlashHelp is the command list shared by the plain REPL and the TUI.
const SlashHelp = `Commands:
  /help                 show this help
  /mode plan|ask|auto   change the approval mode
  /session              print the session id
  /sessions             list saved sessions
  /resume <id>          resume a session (id or unique prefix)
  /clear                start a new session
  /undo                 restore the previous git checkpoint
  /mcp                  list MCP servers and tools
  /model [name]         show or switch the model
  /exit, /quit          leave
`

// SlashEffect is what a UI should do after a slash command. The engine has
// already applied the change. Rebuild means the on-screen transcript should
// be redrawn from Messages, because /clear and /resume replace the conversation.
type SlashEffect struct {
	Quit    bool
	Rebuild bool
	Notice  string
	Mode    string
}

// ApplySlash runs a REPL command. The line must start with "/".
// Unknown commands return an error and change nothing.
func (e *Engine) ApplySlash(line string) (SlashEffect, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return SlashEffect{}, fmt.Errorf("not a slash command")
	}
	switch fields[0] {
	case "/exit", "/quit":
		return SlashEffect{Quit: true}, nil
	case "/help":
		return SlashEffect{Notice: SlashHelp}, nil
	case "/session":
		id := e.SessionID()
		if id == "" {
			return SlashEffect{Notice: "no session"}, nil
		}
		return SlashEffect{Notice: id}, nil
	case "/sessions":
		list, err := e.ListSessions()
		if err != nil {
			return SlashEffect{}, err
		}
		return SlashEffect{Notice: formatSessions(list)}, nil
	case "/mode":
		if len(fields) != 2 {
			return SlashEffect{}, fmt.Errorf("usage: /mode plan|ask|auto")
		}
		if err := e.SetMode(fields[1]); err != nil {
			return SlashEffect{}, err
		}
		return SlashEffect{Notice: "approval mode: " + fields[1], Mode: fields[1]}, nil
	case "/clear":
		id, err := e.Clear()
		if err != nil {
			return SlashEffect{}, err
		}
		return SlashEffect{Notice: "new session " + id, Rebuild: true}, nil
	case "/undo":
		return e.undo()
	case "/model":
		if len(fields) == 1 {
			list := e.Models()
			if len(list) == 0 {
				return SlashEffect{Notice: "model: " + e.ModelName()}, nil
			}
			return SlashEffect{Notice: "model: " + e.ModelName() + "\navailable: " + strings.Join(list, ", ")}, nil
		}
		name := strings.Join(fields[1:], " ")
		if err := e.SetModel(name); err != nil {
			return SlashEffect{}, err
		}
		return SlashEffect{Notice: "model: " + e.ModelName()}, nil
	case "/mcp":
		if e.mcpStatus == nil {
			return SlashEffect{Notice: "no MCP servers configured"}, nil
		}
		return SlashEffect{Notice: e.mcpStatus()}, nil
	case "/resume":
		if len(fields) != 2 {
			return SlashEffect{}, fmt.Errorf("usage: /resume <id>")
		}
		if err := e.Resume(fields[1]); err != nil {
			return SlashEffect{}, err
		}
		return SlashEffect{Notice: "resumed " + e.SessionID(), Rebuild: true}, nil
	default:
		return SlashEffect{}, fmt.Errorf("unknown command %s (try /help)", fields[0])
	}
}

func formatSessions(list []session.Summary) string {
	if len(list) == 0 {
		return "no sessions"
	}
	var b strings.Builder
	b.WriteString("sessions (newest first):")
	for _, s := range list {
		model := s.Model
		if model == "" {
			model = "-"
		}
		preview := s.Preview
		if preview == "" {
			preview = "(no message)"
		}
		fmt.Fprintf(&b, "\n  %s  %s  %s", s.ID, model, preview)
	}
	return b.String()
}
