// Package session runs a long-lived Claude Code process in stream-json mode
// and turns its output into a small set of UI events.
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Event is what the UI receives. Type is one of:
//
//	init        session started; SessionID is set
//	block       a new assistant text block starts
//	text        streamed assistant text (Text)
//	tool        Claude called a tool (Tool, Text = short summary)
//	tool_error  the last tool call failed (Text)
//	done        the turn finished (IsError, Text on error, Cost)
//	exit        the claude process exited (Text = reason)
type Event struct {
	Type      string  `json:"type"`
	Text      string  `json:"text,omitempty"`
	Tool      string  `json:"tool,omitempty"`
	SessionID string  `json:"sessionId,omitempty"`
	IsError   bool    `json:"isError,omitempty"`
	Cost      float64 `json:"cost,omitempty"`
}

// Options configure how claude is started.
type Options struct {
	ClaudePath     string
	Cwd            string
	PermissionMode string
	Model          string
	Env            []string
}

// Session is one Claude Code conversation. The process is started on the
// first Send and resumed by session ID if it dies, so a conversation survives
// the window closing and the process crashing.
type Session struct {
	opts    Options
	onEvent func(Event)

	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	sessionID string
	closed    bool
}

// New creates a session; no process is started until the first Send.
func New(opts Options, onEvent func(Event)) *Session {
	return &Session{opts: opts, onEvent: onEvent}
}

// ID returns the Claude Code session ID once known.
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// Send writes a user message to Claude, starting the process if needed.
func (s *Session) Send(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session is closed")
	}
	if s.cmd == nil {
		if err := s.start(); err != nil {
			return err
		}
	}
	msg := map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": text},
	}
	line, _ := json.Marshal(msg)
	if _, err := s.stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("writing to claude: %w", err)
	}
	return nil
}

// Close stops the claude process. The conversation stays on disk and can
// still be resumed with `claude --resume <id>`.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.cmd != nil {
		s.stdin.Close()
		s.cmd.Process.Kill()
		s.cmd = nil
	}
}

// start launches claude. Callers must hold s.mu.
func (s *Session) start() error {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	}
	if s.opts.PermissionMode != "" {
		args = append(args, "--permission-mode", s.opts.PermissionMode)
	}
	if s.opts.Model != "" {
		args = append(args, "--model", s.opts.Model)
	}
	if s.sessionID != "" {
		args = append(args, "--resume", s.sessionID)
	}

	cmd := exec.Command(s.opts.ClaudePath, args...)
	cmd.Dir = s.opts.Cwd
	cmd.Env = s.opts.Env
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{buf: &stderr, max: 8 << 10}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting claude (%s): %w", s.opts.ClaudePath, err)
	}
	s.cmd = cmd
	s.stdin = stdin

	go s.read(cmd, stdout, &stderr)
	return nil
}

func (s *Session) read(cmd *exec.Cmd, stdout io.Reader, stderr *bytes.Buffer) {
	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			s.handle(line)
		}
		if err != nil {
			break
		}
	}
	waitErr := cmd.Wait()

	s.mu.Lock()
	if s.cmd == cmd {
		s.cmd = nil
	}
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}

	reason := strings.TrimSpace(stderr.String())
	if reason == "" && waitErr != nil {
		reason = waitErr.Error()
	}
	s.onEvent(Event{Type: "exit", Text: reason})
}

// streamMessage covers the fields cmdcmd uses from each stream-json line.
type streamMessage struct {
	Type            string  `json:"type"`
	Subtype         string  `json:"subtype"`
	SessionID       string  `json:"session_id"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	IsError         bool    `json:"is_error"`
	Result          string  `json:"result"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
	Event           struct {
		Type         string `json:"type"`
		ContentBlock struct {
			Type string `json:"type"`
		} `json:"content_block"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type    string          `json:"type"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	IsError bool            `json:"is_error"`
	Content json.RawMessage `json:"content"`
}

func (s *Session) handle(line []byte) {
	var m streamMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return
	}
	// Subagent traffic is not shown; only the main conversation is.
	if m.ParentToolUseID != nil && *m.ParentToolUseID != "" {
		return
	}

	switch m.Type {
	case "system":
		if m.Subtype == "init" {
			s.mu.Lock()
			s.sessionID = m.SessionID
			s.mu.Unlock()
			s.onEvent(Event{Type: "init", SessionID: m.SessionID})
		}

	case "stream_event":
		switch {
		case m.Event.Type == "content_block_start" && m.Event.ContentBlock.Type == "text":
			s.onEvent(Event{Type: "block"})
		case m.Event.Type == "content_block_delta" && m.Event.Delta.Type == "text_delta":
			s.onEvent(Event{Type: "text", Text: m.Event.Delta.Text})
		}

	case "assistant":
		for _, b := range blocks(m.Message.Content) {
			if b.Type == "tool_use" {
				s.onEvent(Event{Type: "tool", Tool: b.Name, Text: summarize(b.Input)})
			}
		}

	case "user":
		for _, b := range blocks(m.Message.Content) {
			if b.Type == "tool_result" && b.IsError {
				s.onEvent(Event{Type: "tool_error", Text: truncate(textOf(b.Content), 300)})
			}
		}

	case "result":
		ev := Event{Type: "done", Cost: m.TotalCostUSD, IsError: m.IsError || m.Subtype != "success"}
		if ev.IsError {
			ev.Text = m.Result
			if ev.Text == "" {
				ev.Text = m.Subtype
			}
		}
		s.onEvent(ev)
	}
}

func blocks(raw json.RawMessage) []contentBlock {
	var bs []contentBlock
	json.Unmarshal(raw, &bs) // plain-string content simply yields no blocks
	return bs
}

// summarize picks the most telling field of a tool's input for display.
func summarize(input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	for _, k := range []string{"description", "command", "skill", "file_path", "pattern", "url", "query", "prompt"} {
		if v, ok := fields[k].(string); ok && v != "" {
			return truncate(v, 120)
		}
	}
	return ""
}

func textOf(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var parts []struct {
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &parts)
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// limitedWriter keeps the last max bytes written, enough for an error message.
type limitedWriter struct {
	mu  sync.Mutex
	buf *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if over := w.buf.Len() - w.max; over > 0 {
		w.buf.Next(over)
	}
	return len(p), nil
}
