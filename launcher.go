package main

import (
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"cmdcmd/internal/config"
	"cmdcmd/internal/hotkey"
	"cmdcmd/internal/session"
	"cmdcmd/internal/wsl"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// The window is a fixed, transparent stage; the visible cards are drawn and
// animated by the frontend inside it, so the window itself never resizes. It
// is larger than the cards so their shadows fit inside it; anything past the
// window edge would be cut off.
const (
	windowWidth  = 880
	windowHeight = 780
	// A window dragged to within this many pixels of the center of its
	// screen snaps to it.
	snapDistance = 40
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
	state        config.State
	dialogOpen   bool           // a system dialog is up; losing focus to it doesn't hide the window
	placed       bool           // the window has been shown once; later it stays where it was moved
	live         []*liveSession // the session on screen and any running in the background
	current      *liveSession
	nextKey      int
	activity     map[string]time.Time // session ID → last status change, for sessions cmdcmd has held
	commands     []session.Command
	claudeModels []session.Model
}

func NewLauncher() *Launcher {
	l := &Launcher{env: config.Env(), activity: map[string]time.Time{}}
	cfg, err := config.Load()
	if err != nil {
		l.notice = "Could not read config: " + err.Error()
	}
	l.cfg = cfg
	wsl.Use(cfg.WSLDistro)
	if l.state, err = config.LoadState(); err != nil {
		l.notice = "Could not read state: " + err.Error()
	}
	if !l.state.HasFolder(l.state.Selected) {
		l.state.Selected = ""
	}
	l.hotkey, err = hotkey.Parse(cfg.Hotkey)
	if err != nil {
		l.notice = err.Error() + "; using the default"
		l.hotkey, _ = hotkey.Parse(hotkey.Default)
	}
	l.newLive("", "", l.scopeCwd())
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
	// Like HideOnFocusLost, except while a dialog cmdcmd opened has focus.
	window.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		l.mu.Lock()
		open := l.dialogOpen
		l.mu.Unlock()
		if !open {
			window.Hide()
		}
	})
	// Dragging empty space moves the window; near the center of its screen
	// it snaps there so it can be put back exactly.
	snapToCenter(window, snapDistance)
	l.startAgent()
	go l.followTranscripts()
	go l.watchSessions()
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
	for _, ls := range l.live {
		if ls.agent != nil {
			ls.agent.Close()
		}
	}
}

// ---------- agents ----------

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

	// An agent is available if it is installed on a side in use.
	installed := func(name, path string) bool {
		return (cfg.UsesWindows() && config.Found(path)) || (cfg.UsesWSL() && wsl.Has(name))
	}
	claude := AgentInfo{ID: "claude", Name: "Claude", Models: claudeModels,
		Available: installed("claude", cfg.FindClaude(l.env))}
	if !claude.Available {
		claude.Reason = "claude CLI not found"
	}

	codex := AgentInfo{ID: "codex", Name: "Codex", Models: session.CodexModels(),
		Available: installed("codex", cfg.FindCodex(l.env))}
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
		custom.Reason = `Set "custom" in the config file`
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
		l.current.agent.Configure(sel.Model, sel.Effort)
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

// Environment is where agents run on Windows: "windows", "wsl" or "both".
// Supported is false on other platforms; Distro is the WSL distro, "" if
// WSL isn't installed.
type Environment struct {
	Supported bool   `json:"supported"`
	Mode      string `json:"mode"`
	Distro    string `json:"distro"`
}

// GetEnvironment returns where agents run.
func (l *Launcher) GetEnvironment() Environment {
	l.mu.Lock()
	mode := l.cfg.Environment
	l.mu.Unlock()
	return Environment{Supported: runtime.GOOS == "windows", Mode: mode, Distro: wsl.Distro()}
}

// SetEnvironment chooses where agents run and saves the choice. If the
// session on screen is on a side no longer shown, a new one replaces it.
func (l *Launcher) SetEnvironment(mode string) error {
	switch mode {
	case config.EnvWindows:
	case config.EnvWSL, config.EnvBoth:
		if !wsl.Available() {
			return errors.New("WSL is not installed")
		}
		wsl.Home() // look it up now rather than while holding l.mu
	default:
		return errors.New("unknown environment " + mode)
	}
	l.mu.Lock()
	changed := l.cfg.Environment != mode
	l.cfg.Environment = mode
	cfg := l.cfg
	hidden := !l.shows(l.current.cwd)
	l.mu.Unlock()
	if !changed {
		return nil
	}
	if hidden {
		l.NewSession()
	}
	err := cfg.Save()
	l.app.Event.Emit("agents:changed")
	l.app.Event.Emit("folders:changed")
	l.app.Event.Emit("sessions:changed")
	return err
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

// ---------- window ----------

// Toggle shows the window if hidden and hides it otherwise.
func (l *Launcher) Toggle() {
	if l.window.IsVisible() {
		l.Hide()
	} else {
		l.Show()
	}
}

// Show brings the window up and focuses the input. The first time, and
// whenever the cursor is on another screen, it is centered on the cursor's
// screen; otherwise it stays where it was dragged.
func (l *Launcher) Show() {
	l.mu.Lock()
	recenter := !l.placed
	l.placed = true
	l.mu.Unlock()
	placeWindow(l.app, l.window, windowWidth, windowHeight, recenter)
	l.window.Show()
	l.window.Focus()
	l.app.Event.Emit("window:shown")
	l.markSeen()
}

// Bounds is a rectangle in window points from the top left.
type Bounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// SetCardBounds tells where the visible card is in the transparent window,
// so dragging can stop it, rather than the window, at the screen's edges.
func (l *Launcher) SetCardBounds(bounds Bounds) {
	setCardBounds(bounds)
}

// Hide hides the window. The agent keeps running.
func (l *Launcher) Hide() {
	l.window.Hide()
	deactivateApp()
}

// Quit exits cmdcmd, stopping the agent.
func (l *Launcher) Quit() {
	l.app.Quit()
}
