// Package session stores an append-only JSONL log of one agent conversation.
//
// Resume rebuilds the message list from "message" events. Approval decisions
// are stored beside them so a session can be audited, and an "always" grant
// can be restored. The log is the source of truth; it is not rewritten.
package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Oasis1i/sigma/internal/provider"
)

const (
	TypeMeta     = "meta"
	TypeMessage  = "message"
	TypeApproval = "approval"
)

// Approval is one allow or deny decision.
type Approval struct {
	Tool     string `json:"tool"`
	Arg      string `json:"arg,omitempty"`
	Action   string `json:"action"`
	Rule     string `json:"rule,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Remember bool   `json:"remember,omitempty"`
}

// Event is one JSONL record.
type Event struct {
	Time     time.Time         `json:"ts"`
	Type     string            `json:"type"`
	Message  *provider.Message `json:"message,omitempty"`
	Approval *Approval         `json:"approval,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
}

// Session is an open log file.
type Session struct {
	ID   string
	Path string

	mu sync.Mutex
	f  *os.File
}

// DefaultDir is $XDG_DATA_HOME/sigma/sessions or ~/.local/share/sigma/sessions.
func DefaultDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory: %w", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "sigma", "sessions"), nil
}

// Create starts a new log and writes the meta event.
func Create(dir string, meta map[string]string) (*Session, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session directory: %w", err)
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	s := &Session{ID: id, Path: path, f: f}
	if meta == nil {
		meta = map[string]string{}
	}
	meta["id"] = id
	if err := s.append(Event{Type: TypeMeta, Meta: meta}); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// Open resumes a session by exact id or a unique prefix.
func Open(dir, id string) (*Session, error) {
	path, err := match(dir, id)
	if err != nil {
		return nil, err
	}
	return openPath(path)
}

// OpenLatest resumes the most recently modified session.
func OpenLatest(dir string) (*Session, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no sessions in %s", dir)
		}
		return nil, err
	}
	var best string
	var bestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestTime) {
			best = filepath.Join(dir, e.Name())
			bestTime = info.ModTime()
		}
	}
	if best == "" {
		return nil, fmt.Errorf("no sessions in %s", dir)
	}
	return openPath(best)
}

func openPath(path string) (*Session, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	return &Session{ID: base, Path: path, f: f}, nil
}

// AppendMessage writes a conversation message.
func (s *Session) AppendMessage(m provider.Message) error {
	msg := m
	return s.append(Event{Type: TypeMessage, Message: &msg})
}

// AppendApproval writes an approval decision.
func (s *Session) AppendApproval(a Approval) error {
	item := a
	return s.append(Event{Type: TypeApproval, Approval: &item})
}

func (s *Session) append(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return fmt.Errorf("session is closed")
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode session event: %w", err)
	}
	if _, err := s.f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	return s.f.Sync()
}

// Events reads the log from disk.
func (s *Session) Events() ([]Event, error) {
	return ReadEvents(s.Path)
}

// Messages rebuilds the conversation. Non-message events are skipped.
func (s *Session) Messages() ([]provider.Message, error) {
	events, err := s.Events()
	if err != nil {
		return nil, err
	}
	var out []provider.Message
	for _, e := range events {
		if e.Type == TypeMessage && e.Message != nil {
			out = append(out, *e.Message)
		}
	}
	return out, nil
}

// RememberedTools returns tools the user allowed with "always".
func (s *Session) RememberedTools() ([]string, error) {
	events, err := s.Events()
	if err != nil {
		return nil, err
	}
	var tools []string
	seen := map[string]bool{}
	for _, e := range events {
		if e.Type != TypeApproval || e.Approval == nil {
			continue
		}
		if e.Approval.Remember && e.Approval.Action == "allow" && !seen[e.Approval.Tool] {
			seen[e.Approval.Tool] = true
			tools = append(tools, e.Approval.Tool)
		}
	}
	return tools, nil
}

// Close closes the log. It is safe to call more than once.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

// ReadEvents parses a JSONL file.
func ReadEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var events []Event
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	return events, nil
}

func newID() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session id: %w", err)
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:]), nil
}

func match(dir, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("session id is required")
	}
	if strings.Contains(id, "/") || strings.Contains(id, `\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	exact := filepath.Join(dir, id+".jsonl")
	if st, err := os.Stat(exact); err == nil && !st.IsDir() {
		return exact, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("sessions: %w", err)
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".jsonl")
		if strings.HasPrefix(base, id) {
			matches = append(matches, filepath.Join(dir, e.Name()))
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no session matching %q in %s", id, dir)
	default:
		return "", fmt.Errorf("session id %q matches %d sessions; pass a longer id", id, len(matches))
	}
}
