package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// Codex is one conversation with the Codex CLI. Codex has no long-lived
// streaming mode, so each message runs `codex exec --json`, and later
// messages continue the same thread with `codex exec resume <id>`.
type Codex struct {
	turns   turnRunner
	onEvent func(Event)

	mu       sync.Mutex
	opts     Options
	threadID string
}

// NewCodex creates a Codex conversation.
func NewCodex(opts Options, onEvent func(Event)) *Codex {
	c := &Codex{opts: opts, onEvent: onEvent, threadID: opts.ResumeID}
	c.turns.run = c.turn
	return c
}

// Start reports Codex's (empty) slash command list; exec mode has none.
func (c *Codex) Start() error {
	c.onEvent(Event{Type: "commands", Commands: []Command{}})
	return nil
}

func (c *Codex) Send(text string, files []string) error {
	c.turns.send(withPaths(text, files))
	return nil
}

func (c *Codex) Configure(model, effort string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opts.Model, c.opts.Effort = model, effort
}

func (c *Codex) Close() { c.turns.close() }

func (c *Codex) turn(text string) {
	c.mu.Lock()
	opts, threadID := c.opts, c.threadID
	c.mu.Unlock()

	args := []string{"exec"}
	if threadID != "" {
		args = append(args, "resume", threadID)
	}
	args = append(args, "--json", "--skip-git-repo-check")
	// -c works for both exec and exec resume, unlike some flags.
	if opts.Model != "" && opts.Model != "default" {
		args = append(args, "-c", fmt.Sprintf("model=%q", opts.Model))
	}
	if opts.Effort != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", opts.Effort))
	}
	if opts.Sandbox != "" {
		args = append(args, "-c", fmt.Sprintf("sandbox_mode=%q", opts.Sandbox))
	}
	args = append(args, "-") // read the prompt from stdin

	cmd := exec.Command(opts.Path, args...)
	cmd.Dir = opts.Cwd
	cmd.Env = opts.Env

	finished := false
	stderr, err := runLines(&c.turns, cmd, text, func(line []byte) {
		if c.handle(line) {
			finished = true
		}
	})
	if finished || c.turns.isClosed() {
		return
	}
	reason := stderr
	if reason == "" && err != nil {
		reason = err.Error()
	}
	c.onEvent(Event{Type: "done", IsError: true, Text: "Codex failed: " + reason})
}

type codexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
	Error    struct {
		Message string `json:"message"`
	} `json:"error"`
	Item struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Command  string `json:"command"`
		ExitCode *int   `json:"exit_code"`
		Output   string `json:"aggregated_output"`
	} `json:"item"`
}

// handle turns one Codex JSONL event into UI events. It reports whether the
// turn has finished.
func (c *Codex) handle(line []byte) bool {
	var e codexEvent
	if json.Unmarshal(bytes.TrimSpace(line), &e) != nil {
		return false
	}
	switch e.Type {
	case "thread.started":
		c.mu.Lock()
		c.threadID = e.ThreadID
		c.mu.Unlock()
		c.onEvent(Event{Type: "init", SessionID: e.ThreadID})
	case "item.started":
		if e.Item.Type == "command_execution" {
			c.onEvent(Event{Type: "tool", Tool: "Shell", Text: truncate(e.Item.Command, 120)})
		}
	case "item.completed":
		switch e.Item.Type {
		case "agent_message":
			c.onEvent(Event{Type: "block"})
			c.onEvent(Event{Type: "text", Text: e.Item.Text})
		case "command_execution":
			if e.Item.ExitCode != nil && *e.Item.ExitCode != 0 {
				c.onEvent(Event{Type: "tool_error", Text: truncate(e.Item.Output, 300)})
			}
		}
	case "turn.completed":
		c.onEvent(Event{Type: "done"})
		return true
	case "turn.failed", "error":
		msg := e.Error.Message
		if msg == "" {
			msg = e.Message
		}
		c.onEvent(Event{Type: "done", IsError: true, Text: msg})
		return true
	}
	return false
}

// CodexModels lists the models Codex offers, from the cache the Codex CLI
// keeps at ~/.codex/models_cache.json. It returns nil if there is no cache.
func CodexModels() []Model {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "models_cache.json"))
	if err != nil {
		return nil
	}
	var cache struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
			Visibility  string `json:"visibility"`
			Levels      []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &cache) != nil {
		return nil
	}
	var models []Model
	for _, m := range cache.Models {
		if m.Visibility != "list" {
			continue
		}
		model := Model{Value: m.Slug, Label: m.DisplayName, Description: m.Description}
		for _, l := range m.Levels {
			model.Efforts = append(model.Efforts, l.Effort)
		}
		models = append(models, model)
	}
	return models
}
