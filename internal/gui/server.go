// Package gui serves the local web UI.
//
// The binary listens on 127.0.0.1 only and embeds the HTML, CSS, and JS with
// embed.FS, so there is no separate web server to install. A random token is
// minted for each launch and put in the URL. Every /api route requires that
// token. The page and the agent therefore cannot be driven by another local
// user, or by a website that talks to 127.0.0.1, unless they know the token.
//
// Events travel as Server-Sent Events. Commands are ordinary POSTs. That
// split needs no websocket library, and a later Wails shell can load the same
// assets and call the same routes. Wails itself is not imported: it needs
// cgo, and the CI build stays pure Go.
package gui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Oasis1i/sigma/internal/auth"
	"github.com/Oasis1i/sigma/internal/engine"
	"github.com/Oasis1i/sigma/internal/provider"
	"github.com/Oasis1i/sigma/internal/tools"
)

//go:embed assets/*
var embedded embed.FS

// assets is the embedded tree with the "assets/" prefix stripped, so a
// request for /app.js maps to the file app.js.
var assets fs.FS

func init() {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		panic("gui assets: " + err.Error())
	}
	assets = sub
}

// Server is one local GUI. Create it with New for tests, or Listen to bind a
// port. Close stops the event pump; it does not close the engine.
type Server struct {
	eng     *engine.Engine
	token   string
	version string

	ctx    context.Context
	cancel context.CancelFunc
	broker *broker

	ln net.Listener
}

// NewToken is 32 random bytes, hex encoded. It is not a session id and it is
// not written to the JSONL log.
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("gui token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// New starts the pump that copies engine events to browsers. token must be
// non-empty. version is shown in the page.
func New(eng *engine.Engine, token, version string) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		eng:     eng,
		token:   token,
		version: version,
		ctx:     ctx,
		cancel:  cancel,
		broker:  newBroker(),
	}
	go s.pump()
	return s
}

// Listen binds 127.0.0.1. port 0 asks the OS for a free port.
func Listen(eng *engine.Engine, token, version string, port int) (*Server, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("port %d is out of range", port)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("gui listen: %w", err)
	}
	s := New(eng, token, version)
	s.ln = ln
	return s, nil
}

// Addr is the host:port actually bound. Empty when New was used without Listen.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// URL is the browser address, including the launch token.
func (s *Server) URL() string {
	return "http://" + s.Addr() + "/?token=" + s.token
}

// Close stops the event pump and the listener. The engine is left running so
// the caller can close it in its own order.
func (s *Server) Close() {
	s.cancel()
	if s.ln != nil {
		_ = s.ln.Close()
	}
}

// Serve accepts connections until ctx is canceled. A canceled context is a
// clean shutdown, not an error.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		return fmt.Errorf("gui server has no listener; call Listen")
	}
	httpSrv := &http.Server{Handler: s.Handler()}
	go func() {
		select {
		case <-ctx.Done():
		case <-s.ctx.Done():
		}
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shut)
	}()
	err := httpSrv.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *Server) pump() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case ev, ok := <-s.eng.Events():
			if !ok {
				return
			}
			s.broker.publish(ev)
		}
	}
}

// Handler is the localhost site. Static files are public so the page can
// explain a missing token. Every /api route checks the token.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.serveFile("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /app.js", s.serveFile("app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /app.css", s.serveFile("app.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /api/state", s.authed(s.handleState))
	mux.HandleFunc("GET /api/sessions", s.authed(s.handleSessions))
	mux.HandleFunc("GET /api/events", s.authed(s.handleEvents))
	mux.HandleFunc("POST /api/message", s.authed(s.handleMessage))
	mux.HandleFunc("POST /api/approval", s.authed(s.handleApproval))
	mux.HandleFunc("POST /api/cancel", s.authed(s.handleCancel))
	mux.HandleFunc("POST /api/mode", s.authed(s.handleMode))
	mux.HandleFunc("POST /api/model", s.authed(s.handleModel))
	mux.HandleFunc("POST /api/resume", s.authed(s.handleResume))
	mux.HandleFunc("POST /api/clear", s.authed(s.handleClear))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// DNS rebinding points an attacker's hostname at 127.0.0.1. The
		// browser would then send that hostname as Host. Reject it.
		if !localHost(r.Host) {
			writeErr(w, http.StatusForbidden, "sigma gui only accepts local connections")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) serveFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
}

func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenMatch(tokenFrom(r), s.token) {
			writeErr(w, http.StatusUnauthorized, "missing or invalid token")
			return
		}
		if !originOK(r) {
			writeErr(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		next(w, r)
	}
}

func tokenFrom(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return r.URL.Query().Get("token")
}

func tokenMatch(got, want string) bool {
	if len(got) == 0 || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

// originOK allows non-browser clients (no Origin header) and same-origin
// browser requests. A page on another site cannot pass this check.
func originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

type stateJSON struct {
	Version          string             `json:"version"`
	Provider         string             `json:"provider"`
	Model            string             `json:"model"`
	Models           []string           `json:"models,omitempty"`
	Auth             []auth.Status      `json:"auth,omitempty"`
	Mode             string             `json:"mode"`
	Workspace        string             `json:"workspace"`
	SessionID        string             `json:"session_id"`
	PromptTokens     int                `json:"prompt_tokens"`
	CompletionTokens int                `json:"completion_tokens"`
	Messages         []provider.Message `json:"messages"`
	Todos            []tools.Todo       `json:"todos,omitempty"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	p, c := s.eng.Usage()
	msgs := s.eng.Messages()
	if msgs == nil {
		msgs = []provider.Message{}
	}
	writeJSON(w, http.StatusOK, stateJSON{
		Version:          s.version,
		Provider:         s.eng.ProviderName(),
		Model:            s.eng.ModelName(),
		Models:           s.eng.Models(),
		Auth:             authSnapshot(),
		Mode:             s.eng.Mode(),
		Workspace:        s.eng.Workspace(),
		SessionID:        s.eng.SessionID(),
		PromptTokens:     p,
		CompletionTokens: c,
		Messages:         msgs,
		Todos:            s.eng.Todos(),
	})
}

type sessionJSON struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Preview string `json:"preview"`
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.eng.ListSessions()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := make([]sessionJSON, 0, len(list))
	for _, item := range list {
		out = append(out, sessionJSON{ID: item.ID, Model: item.Model, Preview: item.Preview})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming is not available")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch, unsub := s.broker.subscribe()
	defer unsub()
	// A comment frame opens the stream so the client knows the connection
	// works before the model has anything to say.
	fmt.Fprintf(w, ": ok\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case ev := <-ch:
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

type slashResult struct {
	Slash   bool   `json:"slash,omitempty"`
	Notice  string `json:"notice,omitempty"`
	Rebuild bool   `json:"rebuild,omitempty"`
	Quit    bool   `json:"quit,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "empty message")
		return
	}
	if strings.HasPrefix(text, "/") && !strings.Contains(text, "\n") {
		effect, err := s.eng.ApplySlash(text)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, slashResult{
			Slash:   true,
			Notice:  effect.Notice,
			Rebuild: effect.Rebuild,
			Quit:    effect.Quit,
			Mode:    s.eng.Mode(),
		})
		return
	}
	s.eng.Submit(engine.Command{Kind: engine.CmdMessage, Text: text})
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string `json:"id"`
		Allow  bool   `json:"allow"`
		Always bool   `json:"always"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ID == "" {
		writeErr(w, http.StatusBadRequest, "approval id is required")
		return
	}
	s.eng.Submit(engine.Command{
		Kind:       engine.CmdApproval,
		ApprovalID: body.ID,
		Allow:      body.Allow,
		Always:     body.Always,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.eng.Cancel()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.eng.Use(body.Provider, body.Model); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"provider": s.eng.ProviderName(),
		"model":    s.eng.ModelName(),
	})
}

func authSnapshot() []auth.Status {
	store, err := auth.DefaultStore()
	if err != nil {
		return nil
	}
	out := make([]auth.Status, 0, len(auth.Known()))
	for _, name := range auth.Known() {
		st, err := store.Status(name)
		if err != nil {
			continue
		}
		out = append(out, st)
	}
	return out
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.eng.SetMode(body.Mode); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": s.eng.Mode()})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.eng.Resume(strings.TrimSpace(body.ID)); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClear(w http.ResponseWriter, r *http.Request) {
	if _, err := s.eng.Clear(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// broker fans one engine stream out to each browser tab. A full subscriber
// buffer drops the event instead of stalling the agent. The dropped event is
// a problem only if a tab stops reading; closing the tab unsubscribes it.
type broker struct {
	mu   sync.Mutex
	subs map[chan engine.Event]struct{}
}

func newBroker() *broker {
	return &broker{subs: map[chan engine.Event]struct{}{}}
}

func (b *broker) subscribe() (<-chan engine.Event, func()) {
	ch := make(chan engine.Event, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

func (b *broker) publish(ev engine.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
