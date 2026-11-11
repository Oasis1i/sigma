// Package mcpclient connects to Model Context Protocol servers and exposes
// their tools to the agent.
//
// A tool named "echo" on a server named "echo" is shown to the model as
// mcp__echo__echo. The approval policy treats it like any other mutating
// tool: in ask mode the user is prompted, because the client cannot know
// whether a remote tool writes somewhere.
//
// Two transports are supported. command runs a process and speaks MCP on its
// stdin and stdout. url uses the streamable HTTP transport. A server that
// fails to connect is reported by /mcp and does not stop the others.
package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Oasis1i/sigma/internal/config"
	"github.com/Oasis1i/sigma/internal/tools"
)

// Status is one configured server, connected or not.
type Status struct {
	Name      string
	Transport string
	Connected bool
	Err       string
	Tools     []string
}

// Manager is the set of live sessions.
type Manager struct {
	statuses []Status
	sessions []*mcp.ClientSession
	toolList []tools.Tool
}

// Connect dials every server. Per-server errors are recorded on Status and
// do not fail the whole call, so one bad URL cannot take the CLI down.
func Connect(ctx context.Context, servers []config.MCPServer) (*Manager, error) {
	m := &Manager{}
	if len(servers) == 0 {
		return m, nil
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "sigma", Version: "0.3.0"}, nil)
	for _, srv := range servers {
		st := Status{Name: srv.Name}
		transport, kind, err := transportFor(srv)
		st.Transport = kind
		if err != nil {
			st.Err = err.Error()
			m.statuses = append(m.statuses, st)
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		session, err := client.Connect(sctx, transport, nil)
		cancel()
		if err != nil {
			st.Err = err.Error()
			m.statuses = append(m.statuses, st)
			continue
		}
		listed, err := session.ListTools(ctx, nil)
		if err != nil {
			st.Err = err.Error()
			_ = session.Close()
			m.statuses = append(m.statuses, st)
			continue
		}
		st.Connected = true
		for _, tool := range listed.Tools {
			if tool == nil || tool.Name == "" {
				continue
			}
			full := ToolName(srv.Name, tool.Name)
			st.Tools = append(st.Tools, tool.Name)
			m.toolList = append(m.toolList, &remoteTool{
				spec: tools.Spec{
					Name:        full,
					Description: tool.Description + " (MCP server " + srv.Name + ")",
					Parameters:  schemaOf(tool.InputSchema),
				},
				session: session,
				remote:  tool.Name,
			})
		}
		m.sessions = append(m.sessions, session)
		m.statuses = append(m.statuses, st)
	}
	return m, nil
}

// ToolName is the name the model sees. Server names cannot contain "__",
// so the split back to server and tool is unambiguous.
func ToolName(server, tool string) string {
	return "mcp__" + server + "__" + tool
}

// Tools are the specs to register. They are not read-only, so ask mode prompts.
func (m *Manager) Tools() []tools.Tool {
	if m == nil {
		return nil
	}
	return append([]tools.Tool(nil), m.toolList...)
}

// Statuses returns a copy of the connection report.
func (m *Manager) Statuses() []Status {
	if m == nil {
		return nil
	}
	return append([]Status(nil), m.statuses...)
}

// Summary is the /mcp listing.
func (m *Manager) Summary() string {
	if m == nil || len(m.statuses) == 0 {
		return "no MCP servers configured"
	}
	var b strings.Builder
	b.WriteString("MCP servers:")
	for _, st := range m.statuses {
		state := "connected"
		if !st.Connected {
			state = "error"
			if st.Err != "" {
				state = "error: " + oneLine(st.Err)
			}
		}
		fmt.Fprintf(&b, "\n  %s  %s  %s", st.Name, st.Transport, state)
		if len(st.Tools) > 0 {
			fmt.Fprintf(&b, "\n    tools: %s", strings.Join(st.Tools, ", "))
		}
	}
	return b.String()
}

// Close closes every session. It is safe on a nil manager.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	for _, s := range m.sessions {
		_ = s.Close()
	}
}

func transportFor(srv config.MCPServer) (mcp.Transport, string, error) {
	switch {
	case srv.URL != "" && len(srv.Command) > 0:
		return nil, "", fmt.Errorf("mcp server %q sets both command and url", srv.Name)
	case srv.URL != "":
		return &mcp.StreamableClientTransport{
			Endpoint:             srv.URL,
			DisableStandaloneSSE: true,
		}, "http", nil
	case len(srv.Command) > 0:
		cmd := exec.Command(srv.Command[0], srv.Command[1:]...)
		if len(srv.Env) > 0 {
			cmd.Env = append(os.Environ(), srv.Env...)
		}
		return &mcp.CommandTransport{Command: cmd}, "stdio", nil
	default:
		return nil, "", fmt.Errorf("mcp server %q needs command or url", srv.Name)
	}
}

func schemaOf(schema any) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if m, ok := schema.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return m
}

type remoteTool struct {
	spec    tools.Spec
	session *mcp.ClientSession
	remote  string
}

func (t *remoteTool) Spec() tools.Spec { return t.spec }

func (t *remoteTool) Execute(ctx context.Context, _ *tools.Env, raw json.RawMessage) (string, error) {
	var args any
	if len(strings.TrimSpace(string(raw))) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	res, err := t.session.CallTool(ctx, &mcp.CallToolParams{Name: t.remote, Arguments: args})
	if err != nil {
		return "", err
	}
	text := contentText(res.Content)
	if res.IsError {
		if text == "" {
			text = "tool failed"
		}
		return "", fmt.Errorf("%s", text)
	}
	if text == "" {
		return "(no content)", nil
	}
	return text, nil
}

func contentText(items []mcp.Content) string {
	var b strings.Builder
	for _, item := range items {
		if text, ok := item.(*mcp.TextContent); ok {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}
