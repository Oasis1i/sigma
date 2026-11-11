package mcpclient

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Oasis1i/sigma/internal/approval"
	"github.com/Oasis1i/sigma/internal/config"
)

func TestMain(m *testing.M) {
	if os.Getenv("SIGMA_MCP_FIXTURE") == "1" {
		runFixture()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type echoIn struct {
	Text string `json:"text"`
}

func runFixture() {
	server := mcp.NewServer(&mcp.Implementation{Name: "sigma-fixture", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "return the text you were given",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: in.Text}},
		}, nil, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}

func TestStdioEcho(t *testing.T) {
	mgr, err := Connect(context.Background(), []config.MCPServer{{
		Name:    "echo",
		Command: []string{os.Args[0]},
		Env:     []string{"SIGMA_MCP_FIXTURE=1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	st := mgr.Statuses()
	if len(st) != 1 || !st[0].Connected || st[0].Transport != "stdio" {
		t.Fatalf("status %+v", st)
	}
	if len(st[0].Tools) != 1 || st[0].Tools[0] != "echo" {
		t.Fatalf("tools %+v", st[0].Tools)
	}
	tools := mgr.Tools()
	if len(tools) != 1 || tools[0].Spec().Name != "mcp__echo__echo" || tools[0].Spec().ReadOnly {
		t.Fatalf("spec %+v", tools[0].Spec())
	}
	out, err := tools[0].Execute(context.Background(), nil, []byte(`{"text":"hello from sigma"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello from sigma" {
		t.Fatalf("output %q", out)
	}
	if !strings.Contains(mgr.Summary(), "echo") || !strings.Contains(mgr.Summary(), "stdio") {
		t.Fatal(mgr.Summary())
	}
}

func TestAskByDefault(t *testing.T) {
	p, err := approval.New("ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	d := p.Decide("mcp__echo__echo", "", false)
	if d.Action != approval.Ask {
		t.Fatalf("MCP tools should ask by default, got %+v", d)
	}
}
