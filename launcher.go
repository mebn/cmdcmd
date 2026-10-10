package main

import (
	"errors"
	"slices"
	"strings"
	"sync"

	"cmdcmd/internal/config"
	"cmdcmd/internal/hotkey"
	"cmdcmd/internal/session"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The window is a fixed, transparent stage; the visible cards are drawn and
// animated by the frontend inside it, so the window itself never resizes. It
// is larger than the cards so their shadows fit inside it; anything past the
// window edge would be cut off.
const (
	windowWidth  = 880
	windowHeight = 780
)

// Launcher is bound to the frontend; its exported methods are callable from JS.
type Launcher struct {
	app    *application.App
	window *application.WebviewWindow
	env    []string
	notice string
	hotkey hotkey.Spec

	mu           sync.Mutex
	cfg          config.Config
	agent        session.Agent
	sessionID    string // the current conversation's ID, once known
	commands     []session.Command
	claudeModels []session.Model
}

func NewLauncher() *Launcher {
	l := &Launcher{env: config.Env()}
	cfg, err := config.Load()
	if err != nil {
		l.notice = "Could not read config: " + err.Error()
	}
	l.cfg = cfg
	l.hotkey, err = hotkey.Parse(cfg.Hotkey)
	if err != nil {
		l.notice = err.Error() + "; using the default"
		l.hotkey, _ = hotkey.Parse(hotkey.Default)
	}
	l.agent = l.newAgent()
	return l
}

// ShortcutLabel is the hotkey as shown to the user, e.g. "⌘ ⌘".
func (l *Launcher) ShortcutLabel() string {
	return l.hotkey.Label
}

// attach connects the launcher to the running app, asks for the permissions
// cmdcmd needs, and starts the hotkey.
func (l *Launcher) attach(app *application.App, window *application.WebviewWindow) {
	l.app = app
	l.window = window
	l.startAgent()
	err := l.startHotkey()
	// Folder prompts come after the Input Monitoring prompt so they queue up
	// behind it instead of covering it.
	go requestFolderAccess()
	if err != nil {
		l.setNotice(err.Error())
		l.Show() // the hotkey can't open the window, so show it now
	}
}

// startHotkey listens for the configured hotkey: a double tap through the
// keyboard hook, or a key combination through a global shortcut.
func (l *Launcher) startHotkey() error {
	if l.hotkey.DoubleTap {
		return hotkey.Start(l.hotkey.Modifier, l.Toggle, func() {
			// Input Monitoring was granted while running; the hotkey is live.
			l.setNotice("")
		})
	}
	return l.app.GlobalShortcut.Register(l.hotkey.Accelerator, func() { go l.Toggle() })
}

func (l *Launcher) setNotice(notice string) {
	l.mu.Lock()
	l.notice = notice
	l.mu.Unlock()
	l.app.Event.Emit("notice", notice)
}

// OpenPermissionSettings opens the system settings pane for the hotkey
// permission.
func (l *Launcher) OpenPermissionSettings() {
	if hotkey.SettingsURL != "" {
		openURL(hotkey.SettingsURL)
	}
}

func (l *Launcher) shutdown() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.agent.Close()
}

// ---------- agents ----------

// newAgent creates the agent selected in the config. Callers must hold l.mu
// (or be the constructor).
func (l *Launcher) newAgent() session.Agent {
	return l.newAgentResuming("")
}

// newAgentResuming creates the selected agent, continuing the session with
// the given ID if it is not empty. Callers must hold l.mu.
func (l *Launcher) newAgentResuming(resumeID string) session.Agent {
	cfg := l.cfg
	opts := session.Options{
		Cwd:      cfg.ResolvedCwd(),
		Env:      l.env,
		Model:    cfg.Model,
		Effort:   cfg.Effort,
		ResumeID: resumeID,
	}
	var a session.Agent
	onEvent := func(ev session.Event) { l.onAgentEvent(a, ev) }
	switch cfg.Agent {
	case "codex":
		opts.Path = cfg.FindCodex(l.env)
		opts.Sandbox = cfg.CodexSandbox
		a = session.NewCodex(opts, onEvent)
	case "custom":
		opts.Path, opts.Args = config.Shell(cfg.Custom.Command)
		a = session.NewCustom(opts, onEvent)
	default:
		opts.Path = cfg.FindClaude(l.env)
		opts.PermissionMode = cfg.PermissionMode
		a = session.NewClaude(opts, onEvent)
	}
	return a
}

func (l *Launcher) onAgentEvent(a session.Agent, ev session.Event) {
	// Drop events from an agent that was replaced.
	l.mu.Lock()
	current := l.agent == a
	if current && ev.Type == "init" {
		l.sessionID = ev.SessionID
	}
	if current && ev.Type == "commands" {
		l.commands = ev.Commands
		if len(ev.Models) > 0 {
			l.claudeModels = ev.Models
		}
	}
	l.mu.Unlock()
	if !current || l.app == nil {
		return
	}
	l.app.Event.Emit("claude", ev)
	if len(ev.Models) > 0 {
		l.app.Event.Emit("agents:changed")
	}
}

// startAgent prepares the agent in the background: Claude starts warm so the
// first reply is faster and the slash command list arrives early.
func (l *Launcher) startAgent() {
	l.mu.Lock()
	a := l.agent
	l.mu.Unlock()
	go func() {
		if err := a.Start(); err != nil {
			l.app.Event.Emit("claude", session.Event{Type: "exit", Text: err.Error()})
		}
	}()
}

// AgentInfo describes an agent for the picker.
type AgentInfo struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Available bool            `json:"available"`
	Reason    string          `json:"reason,omitempty"`
	Models    []session.Model `json:"models"`
	Command   string          `json:"command,omitempty"` // custom agent only
}

// Selection is the chosen agent, model and effort ("" = default).
type Selection struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// fallbackClaudeModels are shown until Claude reports its own list.
var fallbackClaudeModels = []session.Model{
	{Value: "default", Label: "Default (recommended)", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
	{Value: "opus", Label: "Opus", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
	{Value: "sonnet", Label: "Sonnet", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
	{Value: "haiku", Label: "Haiku", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
}

// GetAgents lists the agents and their models.
func (l *Launcher) GetAgents() []AgentInfo {
	l.mu.Lock()
	cfg, claudeModels := l.cfg, l.claudeModels
	l.mu.Unlock()
	if len(claudeModels) == 0 {
		claudeModels = fallbackClaudeModels
	}

	claude := AgentInfo{ID: "claude", Name: "Claude", Models: claudeModels,
		Available: config.Found(cfg.FindClaude(l.env))}
	if !claude.Available {
		claude.Reason = "claude CLI not found"
	}

	codex := AgentInfo{ID: "codex", Name: "Codex", Models: session.CodexModels(),
		Available: config.Found(cfg.FindCodex(l.env))}
	if !codex.Available {
		codex.Reason = "codex CLI not found"
	}
	// "Default" leaves the model to ~/.codex/config.toml.
	codex.Models = append([]session.Model{{
		Value:       "default",
		Label:       "Default",
		Description: "The model set in ~/.codex/config.toml",
		Efforts:     commonEfforts(codex.Models),
	}}, codex.Models...)

	name := cfg.Custom.Name
	if name == "" {
		name = "Custom"
	}
	custom := AgentInfo{ID: "custom", Name: name, Available: cfg.Custom.Command != "",
		Models: []session.Model{}, Command: cfg.Custom.Command}
	if !custom.Available {
		custom.Reason = "Enter a command below"
	}
	return []AgentInfo{claude, codex, custom}
}

// commonEfforts returns the effort levels every model supports, in order.
func commonEfforts(models []session.Model) []string {
	if len(models) == 0 {
		return nil
	}
	var out []string
	for _, e := range models[0].Efforts {
		all := true
		for _, m := range models[1:] {
			if !slices.Contains(m.Efforts, e) {
				all = false
				break
			}
		}
		if all {
			out = append(out, e)
		}
	}
	return out
}

// GetSelection returns the current agent, model and effort.
func (l *Launcher) GetSelection() Selection {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Selection{Agent: l.cfg.Agent, Model: l.cfg.Model, Effort: l.cfg.Effort}
}

// SetSelection switches agent, model or effort and saves the choice. A new
// agent starts a new conversation; a new model or effort keeps it.
func (l *Launcher) SetSelection(sel Selection) error {
	if sel.Agent != "claude" && sel.Agent != "codex" && sel.Agent != "custom" {
		return errors.New("unknown agent " + sel.Agent)
	}
	l.mu.Lock()
	agentChanged := l.cfg.Agent != sel.Agent
	l.cfg.Agent, l.cfg.Model, l.cfg.Effort = sel.Agent, sel.Model, sel.Effort
	cfg := l.cfg
	if !agentChanged {
		l.agent.Configure(sel.Model, sel.Effort)
	}
	l.mu.Unlock()

	if agentChanged {
		l.NewSession()
	}
	return cfg.Save()
}

// SetCustomCommand saves the custom agent's shell command. If the custom
// agent is in use, it restarts with the new command.
func (l *Launcher) SetCustomCommand(command string) error {
	command = strings.TrimSpace(command)
	l.mu.Lock()
	changed := l.cfg.Custom.Command != command
	l.cfg.Custom.Command = command
	inUse := l.cfg.Agent == "custom"
	cfg := l.cfg
	l.mu.Unlock()
	if !changed {
		return nil
	}
	if inUse {
		l.NewSession()
	}
	return cfg.Save()
}

// GetCommands returns the slash commands the current agent accepts.
func (l *Launcher) GetCommands() []session.Command {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.commands
}

// Info describes the session for the UI.
type Info struct {
	Shortcut        string `json:"shortcut"`
	Cwd             string `json:"cwd"`
	Notice          string `json:"notice"`
	CanOpenSettings bool   `json:"canOpenSettings"`
}

// GetInfo returns static details for the UI.
func (l *Launcher) GetInfo() Info {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Info{
		Shortcut:        l.hotkey.Label,
		Cwd:             l.cfg.ResolvedCwd(),
		Notice:          l.notice,
		CanOpenSettings: hotkey.SettingsURL != "",
	}
}

// Send sends a message to the current agent.
func (l *Launcher) Send(text string) error {
	l.mu.Lock()
	a := l.agent
	l.mu.Unlock()
	return a.Send(text)
}

// NewSession ends the current conversation and starts a fresh one.
func (l *Launcher) NewSession() {
	l.mu.Lock()
	l.agent.Close()
	l.agent = l.newAgent()
	l.sessionID = ""
	l.commands = nil
	l.mu.Unlock()
	if l.app != nil {
		l.app.Event.Emit("session:new")
		l.startAgent()
	}
}

// ---------- past sessions ----------

// SessionList is the current agent's recent sessions and which one is open.
type SessionList struct {
	Current  string                `json:"current"`
	Sessions []session.SessionInfo `json:"sessions"`
}

// GetSessions lists the current agent's recent sessions, newest first.
func (l *Launcher) GetSessions() SessionList {
	l.mu.Lock()
	agent, cwd, current := l.cfg.Agent, l.cfg.ResolvedCwd(), l.sessionID
	l.mu.Unlock()
	list := SessionList{Current: current, Sessions: []session.SessionInfo{}}
	switch agent {
	case "claude":
		list.Sessions = append(list.Sessions, session.ClaudeSessions(cwd)...)
	case "codex":
		list.Sessions = append(list.Sessions, session.CodexSessions()...)
	}
	return list
}

// ResumeSession switches to a past session of the current agent and sends
// its messages to the UI as a "session:resumed" event.
func (l *Launcher) ResumeSession(id string) {
	l.mu.Lock()
	agent, cwd := l.cfg.Agent, l.cfg.ResolvedCwd()
	if id == l.sessionID || agent == "custom" {
		l.mu.Unlock()
		return
	}
	l.agent.Close()
	l.agent = l.newAgentResuming(id)
	l.sessionID = id
	l.commands = nil
	l.mu.Unlock()

	var history []session.HistoryItem
	if agent == "claude" {
		history = session.ClaudeHistory(cwd, id)
	} else {
		history = session.CodexHistory(id)
	}
	if history == nil {
		history = []session.HistoryItem{}
	}
	l.app.Event.Emit("session:resumed", history)
	l.startAgent()
}

// ---------- window ----------

// Toggle shows the window if hidden and hides it otherwise.
func (l *Launcher) Toggle() {
	if l.window.IsVisible() {
		l.Hide()
	} else {
		l.Show()
	}
}

// Show brings the window up, centered on the screen the cursor is on, and
// focuses the input.
func (l *Launcher) Show() {
	placeWindow(l.app, l.window, windowWidth, windowHeight)
	l.window.Show()
	l.window.Focus()
	l.app.Event.Emit("window:shown")
}

// Hide hides the window. The agent keeps running.
func (l *Launcher) Hide() {
	l.window.Hide()
}

// Quit exits cmdcmd, stopping the agent.
func (l *Launcher) Quit() {
	l.app.Quit()
}
