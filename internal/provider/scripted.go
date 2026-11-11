package provider

import (
	"context"
	"fmt"
	"sync"
)

// Scripted is a fake Client that returns a fixed list of responses.
// It records the messages it was shown so tests can check the loop.
type Scripted struct {
	mu        sync.Mutex
	Responses []Response
	Seen      [][]Message
	n         int
	model     string
}

// SetModel implements ModelSwitch.
func (s *Scripted) SetModel(name string) {
	s.mu.Lock()
	s.model = name
	s.mu.Unlock()
}

// CurrentModel implements ModelSwitch.
func (s *Scripted) CurrentModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model
}

// Complete implements Client.
func (s *Scripted) Complete(ctx context.Context, messages []Message, tools []ToolSpec, onDelta func(string)) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]Message, len(messages))
	copy(cp, messages)
	s.Seen = append(s.Seen, cp)
	if s.n >= len(s.Responses) {
		return Response{}, fmt.Errorf("scripted provider has no response for call %d", s.n+1)
	}
	resp := s.Responses[s.n]
	s.n++
	if onDelta != nil && resp.Content != "" {
		onDelta(resp.Content)
	}
	return resp, nil
}
