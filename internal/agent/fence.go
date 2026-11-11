package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Oasis1i/sigma/internal/provider"
)

// fenceRE finds fenced shell blocks. A model that does not emit native tool
// calls can still request work the way mini-swe-agent does, by writing bash.
// Other languages are left alone.
var fenceRE = regexp.MustCompile("(?s)```(?:bash|sh|shell)[ \\t]*\\r?\\n(.*?)```")

func bashFences(content string) []provider.ToolCall {
	matches := fenceRE.FindAllStringSubmatch(content, -1)
	var calls []provider.ToolCall
	for _, m := range matches {
		cmd := strings.TrimRight(m[1], "\n")
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		args, err := json.Marshal(map[string]string{"command": cmd})
		if err != nil {
			continue
		}
		calls = append(calls, provider.ToolCall{
			ID:        fmt.Sprintf("fallback_bash_%d", len(calls)+1),
			Name:      "bash",
			Arguments: string(args),
		})
	}
	return calls
}
