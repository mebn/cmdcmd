// Package session runs AI agent CLIs (Claude Code, Codex, or a custom
// command) in the background and turns their output into a small set of UI
// events.
package session

// Event is what the UI receives. Type is one of:
//
//	init        session started; SessionID is set
//	commands    the available slash commands (Commands)
//	block       a new assistant text block starts
//	text        streamed assistant text (Text)
//	tool        the agent ran a tool (Tool, Text = short summary)
//	tool_error  the last tool call failed (Text)
//	done        the turn finished (IsError, Text on error, Cost)
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
	Cwd    string
	Env    []string
	Model  string
	Effort string

	PermissionMode string // claude --permission-mode
	Sandbox        string // codex sandbox_mode
}

// Agent is one conversation with an agent CLI.
type Agent interface {
	// Start prepares the agent ahead of the first message, when that helps.
	Start() error
	// Send sends a user message; replies arrive as events.
	Send(text string) error
	// Configure switches model and effort, keeping the conversation.
	Configure(model, effort string)
	// Close stops the agent.
	Close()
}
