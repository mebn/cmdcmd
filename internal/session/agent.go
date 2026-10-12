// Package session runs AI agent CLIs (Claude Code, Codex, or a custom
// command) in the background and turns their output into a small set of UI
// events.
package session

import (
	"os/exec"

	"cmdcmd/internal/wsl"
)

// Event is what the UI receives. Type is one of:
//
//	init        session started; SessionID is set
//	start       a turn started that cmdcmd didn't send (Claude reacting to a
//	            finished background task)
//	tasks       the background tasks now running (Tasks)
//	commands    the available slash commands (Commands)
//	block       a new assistant text block starts
//	text        streamed assistant text (Text)
//	tool        the agent ran a tool (Tool, Text = short summary)
//	tool_error  the last tool call failed (Text)
//	done        the turn finished (IsError, Text on error, Cost); Idle when
//	            nothing is left running, whatever was sent meanwhile
//	exit        the agent process exited unexpectedly (Text = reason)
type Event struct {
	Type      string    `json:"type"`
	Text      string    `json:"text,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
	IsError   bool      `json:"isError,omitempty"`
	Cost      float64   `json:"cost,omitempty"`
	Commands  []Command `json:"commands,omitempty"`
	Models    []Model   `json:"models,omitempty"`
	Tasks     []Task    `json:"tasks,omitempty"`
	Idle      bool      `json:"idle,omitempty"`
}

// Task is a background task an agent has running, such as a shell.
type Task struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // "local_bash", "local_agent", …
	Description string `json:"description"`
}

// Command is a slash command or skill accepted as "/name args".
type Command struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint,omitempty"`
}

// Model is a model an agent can run, with the effort levels it supports.
type Model struct {
	Value       string   `json:"value"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Efforts     []string `json:"efforts,omitempty"`
}

// Options configure how an agent CLI is started.
type Options struct {
	Path   string // the CLI binary
	Args   []string
	Cwd    string // a Linux path when Distro is set
	Env    []string
	Model  string
	Effort string

	// ResumeID continues an existing session (Claude) or thread (Codex)
	// instead of starting a new one.
	ResumeID string

	PermissionMode string // claude --permission-mode
	Sandbox        string // codex sandbox_mode

	// Distro runs the agent in this WSL distro through wsl.exe, finding Path
	// on the PATH of the user's login shell there.
	Distro string
}

// command builds the process for the agent CLI with the given arguments,
// in opts.Cwd, on Windows or in WSL.
func (opts Options) command(args ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if opts.Distro != "" {
		program, wslArgs := wsl.Command(opts.Distro, opts.Cwd, opts.Path, args...)
		cmd = exec.Command(program, wslArgs...)
	} else {
		cmd = exec.Command(opts.Path, args...)
		cmd.Dir = opts.Cwd
	}
	cmd.Env = opts.Env
	return cmd
}

// Agent is one conversation with an agent CLI.
type Agent interface {
	// Start prepares the agent ahead of the first message, when that helps.
	Start() error
	// Send sends a user message with any attached files (absolute paths);
	// replies arrive as events.
	Send(text string, files []string) error
	// Configure switches model and effort, keeping the conversation.
	Configure(model, effort string)
	// Close stops the agent.
	Close()
}

// HideWindow keeps Windows from flashing a console window for cmd.
func HideWindow(cmd *exec.Cmd) { hideWindow(cmd) }
