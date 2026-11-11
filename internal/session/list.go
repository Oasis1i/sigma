package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Summary is one session file, newest-friendly. Preview is the first user
// message, trimmed to a single line so a picker can show it.
type Summary struct {
	ID      string
	Path    string
	ModTime time.Time
	Model   string
	Preview string
}

// List returns the session logs in dir, newest first. A missing directory is
// an empty list, not an error: a fresh install has nothing to resume.
func List(dir string) ([]Summary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Summary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		model, preview := peek(path)
		out = append(out, Summary{
			ID:      strings.TrimSuffix(e.Name(), ".jsonl"),
			Path:    path,
			ModTime: info.ModTime(),
			Model:   model,
			Preview: preview,
		})
	}
	// Newest first. Equal timestamps keep the directory order, which is stable
	// enough for a picker and avoids a sort that depends on the clock resolution.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ModTime.After(out[j-1].ModTime); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// peek reads only the start of the log. The model name is in the meta event
// and the preview is the first user message, both of which are written before
// the conversation grows large.
func peek(path string) (model, preview string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		if n > 40 {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.Type == TypeMeta && e.Meta != nil && model == "" {
			model = e.Meta["model"]
		}
		if e.Type == TypeMessage && e.Message != nil && e.Message.Role == "user" && preview == "" && IsRealUserPrompt(e.Message.Content) {
			preview = oneLine(e.Message.Content, 80)
		}
		if model != "" && preview != "" {
			break
		}
	}
	return model, preview
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
