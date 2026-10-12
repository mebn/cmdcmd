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

// Claude is one Claude Code conversation in a long-lived `claude -p`
// stream-json process. The process is resumed by session ID if it dies or is
// restarted with new settings, so the conversation survives both.
type Claude struct {
	opts    Options
	onEvent func(Event)

	mu             sync.Mutex
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	sessionID      string
	closed         bool
	busy           int  // turns sent but not finished
	restartPending bool // apply new settings once the current turn ends
}

// NewClaude creates a session; no process is started until Start or Send.
func NewClaude(opts Options, onEvent func(Event)) *Claude {
	return &Claude{opts: opts, onEvent: onEvent, sessionID: opts.ResumeID}
}

// ID returns the Claude Code session ID once known.
func (s *Claude) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// Start launches the claude process ahead of the first message, so the
// first reply is faster and the slash command list arrives early.
func (s *Claude) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureStarted()
}

// Send writes a user message to Claude, starting the process if needed.
// Images go along inline; other files are named by path for Claude to read.
func (s *Claude) Send(text string, files []string) error {
	var images []map[string]any
	var others []string
	for _, f := range files {
		if img := imageBlock(f); img != nil {
			images = append(images, img)
		} else {
			others = append(others, f)
		}
	}
	var content any = withPaths(text, others)
	if len(images) > 0 {
		blocks := images
		if t := content.(string); t != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": t})
		}
		content = blocks
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureStarted(); err != nil {
		return err
	}
	s.busy++
	return s.write(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": content},
	})
}

// Configure switches model and effort. claude takes these only at launch, so
// the process restarts with --resume and the conversation carries on; if a
// turn is running, the restart waits until it ends.
func (s *Claude) Configure(model, effort string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.Model, s.opts.Effort = model, effort
	if s.cmd == nil {
		return
	}
	if s.busy > 0 {
		s.restartPending = true
		return
	}
	s.restart()
}

// restart stops claude and starts it again with the current options.
// Callers must hold s.mu.
func (s *Claude) restart() {
	s.restartPending = false
	s.stop()
	if err := s.ensureStarted(); err != nil {
		go s.onEvent(Event{Type: "exit", Text: err.Error()})
	}
}

// stop kills the process without reporting an exit. Callers must hold s.mu.
func (s *Claude) stop() {
	if s.cmd != nil {
		s.stdin.Close()
		s.cmd.Process.Kill()
		s.cmd = nil
	}
}

// ensureStarted starts claude if it is not running. Callers must hold s.mu.
func (s *Claude) ensureStarted() error {
	if s.closed {
		return errors.New("session is closed")
	}
	if s.cmd != nil {
		return nil
	}
	if err := s.start(); err != nil {
		return err
	}
	// The same handshake the Agent SDK does; the reply lists every slash
	// command and skill with its description.
	return s.write(map[string]any{
		"type":       "control_request",
		"request_id": initRequestID,
		"request":    map[string]any{"subtype": "initialize"},
	})
}

const initRequestID = "cmdcmd-init"

// write sends one stream-json line. Callers must hold s.mu.
func (s *Claude) write(msg any) error {
	line, _ := json.Marshal(msg)
	if _, err := s.stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("writing to claude: %w", err)
	}
	return nil
}

// Close stops the claude process. The conversation stays on disk and can
// still be resumed with `claude --resume <id>`.
func (s *Claude) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.stop()
}

// start launches claude. Callers must hold s.mu.
func (s *Claude) start() error {
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
	if s.opts.Model != "" && s.opts.Model != "default" {
		args = append(args, "--model", s.opts.Model)
	}
	if s.opts.Effort != "" {
		args = append(args, "--effort", s.opts.Effort)
	}
	if s.sessionID != "" {
		args = append(args, "--resume", s.sessionID)
	}

	cmd := s.opts.command(args...)
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
		return fmt.Errorf("starting claude (%s): %w", s.opts.Path, err)
	}
	s.cmd = cmd
	s.stdin = stdin

	go s.read(cmd, stdout, &stderr)
	return nil
}

func (s *Claude) read(cmd *exec.Cmd, stdout io.Reader, stderr *bytes.Buffer) {
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

	// A process that was stopped on purpose (Close, restart) is not reported.
	s.mu.Lock()
	current := s.cmd == cmd
	if current {
		s.cmd = nil
		s.busy = 0
	}
	s.mu.Unlock()
	if !current {
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
	Response        struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Response  struct {
			Commands []Command `json:"commands"`
			Models   []struct {
				Value       string   `json:"value"`
				DisplayName string   `json:"displayName"`
				Description string   `json:"description"`
				Efforts     []string `json:"supportedEffortLevels"`
			} `json:"models"`
		} `json:"response"`
	} `json:"response"`
	Event struct {
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
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	IsError bool            `json:"is_error"`
	Content json.RawMessage `json:"content"`
}

func (s *Claude) handle(line []byte) {
	var m streamMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return
	}
	// Subagent traffic is not shown; only the main conversation is.
	if m.ParentToolUseID != nil && *m.ParentToolUseID != "" {
		return
	}

	switch m.Type {
	case "control_response":
		if m.Response.RequestID == initRequestID && m.Response.Subtype == "success" {
			var models []Model
			for _, md := range m.Response.Response.Models {
				models = append(models, Model{Value: md.Value, Label: md.DisplayName, Description: md.Description, Efforts: md.Efforts})
			}
			s.onEvent(Event{Type: "commands", Commands: visibleCommands(m.Response.Response.Commands), Models: models})
		}

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
		s.mu.Lock()
		if s.busy > 0 {
			s.busy--
		}
		if s.busy == 0 && s.restartPending {
			s.restart()
		}
		s.mu.Unlock()
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

// visibleCommands drops internal commands, whose names start with "__".
func visibleCommands(all []Command) []Command {
	out := make([]Command, 0, len(all))
	for _, c := range all {
		if !strings.HasPrefix(c.Name, "__") {
			out = append(out, c)
		}
	}
	return out
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
