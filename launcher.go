package main

import (
	"sync"

	"cmdcmd/internal/config"
	"cmdcmd/internal/hotkey"
	"cmdcmd/internal/session"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Launcher is bound to the frontend; its exported methods are callable from JS.
type Launcher struct {
	app    *application.App
	window *application.WebviewWindow
	opts   session.Options
	notice string

	mu      sync.Mutex
	session *session.Session
}

func NewLauncher() *Launcher {
	l := &Launcher{}
	cfg, err := config.Load()
	if err != nil {
		l.notice = "Could not read config: " + err.Error()
	}
	env := config.Env()
	l.opts = session.Options{
		ClaudePath:     cfg.FindClaude(env),
		Cwd:            cfg.ResolvedCwd(),
		PermissionMode: cfg.PermissionMode,
		Model:          cfg.Model,
		Env:            env,
	}
	l.session = l.newSession()
	return l
}

// attach connects the launcher to the running app, asks for the permissions
// cmdcmd needs, and starts the hotkey.
func (l *Launcher) attach(app *application.App, window *application.WebviewWindow) {
	l.app = app
	l.window = window
	err := hotkey.Start(l.Toggle, func() {
		// Input Monitoring was granted while running; the hotkey is live now.
		l.setNotice("")
	})
	// Folder prompts come after the Input Monitoring prompt so they queue up
	// behind it instead of covering it.
	go requestFolderAccess()
	if err != nil {
		l.setNotice(err.Error())
		l.Show() // the hotkey can't open the window yet, so show it now
	}
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
	l.session.Close()
}

func (l *Launcher) newSession() *session.Session {
	var s *session.Session
	s = session.New(l.opts, func(ev session.Event) {
		// Drop events from a session that was replaced by NewSession.
		l.mu.Lock()
		current := l.session == s
		l.mu.Unlock()
		if current && l.app != nil {
			l.app.Event.Emit("claude", ev)
		}
	})
	return s
}

// Info is shown in the window header.
type Info struct {
	Shortcut        string `json:"shortcut"`
	Cwd             string `json:"cwd"`
	PermissionMode  string `json:"permissionMode"`
	Notice          string `json:"notice"`
	CanOpenSettings bool   `json:"canOpenSettings"`
}

// GetInfo returns static details for the UI.
func (l *Launcher) GetInfo() Info {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Info{
		Shortcut:        hotkey.Description,
		Cwd:             l.opts.Cwd,
		PermissionMode:  l.opts.PermissionMode,
		Notice:          l.notice,
		CanOpenSettings: hotkey.SettingsURL != "",
	}
}

// Send sends a message to the current Claude session.
func (l *Launcher) Send(text string) error {
	l.mu.Lock()
	s := l.session
	l.mu.Unlock()
	return s.Send(text)
}

// NewSession ends the current conversation and starts a fresh one.
func (l *Launcher) NewSession() {
	l.mu.Lock()
	l.session.Close()
	l.session = l.newSession()
	l.mu.Unlock()
	if l.app != nil {
		l.app.Event.Emit("session:new")
	}
}

// Toggle shows the window if hidden and hides it otherwise.
func (l *Launcher) Toggle() {
	if l.window.IsVisible() {
		l.Hide()
	} else {
		l.Show()
	}
}

// Show brings the window up on the screen the cursor is on and focuses the
// input.
func (l *Launcher) Show() {
	l.placeOnCursorScreen()
	l.window.Show()
	l.window.Focus()
	l.app.Event.Emit("window:shown")
}

// Hide hides the window. The Claude session keeps running.
func (l *Launcher) Hide() {
	l.window.Hide()
}

// Quit exits cmdcmd, stopping the Claude process.
func (l *Launcher) Quit() {
	l.app.Quit()
}

// placeOnCursorScreen puts the window in the upper third of the screen the
// cursor is on, like Spotlight.
func (l *Launcher) placeOnCursorScreen() {
	screen := l.app.Screen.GetByID(cursorScreenID())
	if screen == nil {
		screen = l.app.Screen.GetPrimary()
	}
	if screen == nil {
		l.window.Center()
		return
	}
	l.window.SetScreen(screen)
	w, h := l.window.Size()
	area := screen.WorkArea
	y := area.Height/4 - h/2
	if y < 0 {
		y = 0
	}
	l.window.SetRelativePosition((area.Width-w)/2, y)
}
