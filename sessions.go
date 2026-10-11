package main

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"time"

	"cmdcmd/internal/config"
	"cmdcmd/internal/session"
)

// Session statuses, shown as a symbol in the sessions bar.
const (
	statusIdle    = "idle"    // nothing going on
	statusRunning = "running" // a turn is in progress
	statusWaiting = "waiting" // finished while you were not looking; waiting for you
	statusError   = "error"   // the last turn failed or the agent stopped
)

// liveSession is a conversation cmdcmd is holding: the one on screen, or one
// whose turn is still running (or finished unseen) in the background. A
// background session's agent is closed once its turn ends; it is resumed if
// you switch back to it.
type liveSession struct {
	key     string        // local ID, known before the agent reports its own
	kind    string        // "claude", "codex" or "custom"
	agent   session.Agent // nil once closed
	id      string        // the agent's session ID, once known
	title   string
	updated time.Time // when the status last changed; the bar sorts by it
	status  string
	busy    int    // turns sent but not finished
	shell   string // output of ! commands, sent along with the next message
}

// AgentEvent is an agent event tagged with the session it belongs to.
type AgentEvent struct {
	Session string `json:"session"`
	session.Event
}

// OpenedSession tells the UI which session is now on screen. History is set
// only for a session resumed from disk; the UI already holds the others.
type OpenedSession struct {
	Key     string                `json:"key"`
	History []session.HistoryItem `json:"history"`
}

// SessionItem is one entry of the sessions bar: a live session (Key set), a
// past one on disk (ID set), or both.
type SessionItem struct {
	Key     string    `json:"key"`
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
	Status  string    `json:"status"`
}

// SessionList is the current agent's sessions, newest first, and the key of
// the one on screen.
type SessionList struct {
	Current  string        `json:"current"`
	Sessions []SessionItem `json:"sessions"`
}

// newLive creates a session for the selected agent, continuing the session
// with the given ID if it is not empty, and makes it current. Callers must
// hold l.mu.
func (l *Launcher) newLive(resumeID, title string) *liveSession {
	l.nextKey++
	ls := &liveSession{
		key:     fmt.Sprint("s", l.nextKey),
		kind:    l.cfg.Agent,
		id:      resumeID,
		title:   title,
		updated: time.Now(),
		status:  statusIdle,
	}
	ls.agent = l.newAgent(ls, resumeID)
	l.live = append(l.live, ls)
	l.current = ls
	l.commands = nil
	return ls
}

// newAgent creates the selected agent for ls, continuing the session with
// the given ID if it is not empty. Callers must hold l.mu.
func (l *Launcher) newAgent(ls *liveSession, resumeID string) session.Agent {
	cfg := l.cfg
	opts := session.Options{
		Cwd:      cfg.ResolvedCwd(),
		Env:      l.env,
		Model:    cfg.Model,
		Effort:   cfg.Effort,
		ResumeID: resumeID,
	}
	var a session.Agent
	onEvent := func(ev session.Event) { l.onAgentEvent(ls, a, ev) }
	switch ls.kind {
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

func (l *Launcher) onAgentEvent(ls *liveSession, a session.Agent, ev session.Event) {
	// A turn that ends while the window is hidden goes unseen even on screen.
	finished := ev.Type == "done" || ev.Type == "exit"
	visible := finished && l.window != nil && l.window.IsVisible()
	l.mu.Lock()
	// Drop events from an agent that was replaced or closed.
	if ls.agent != a {
		l.mu.Unlock()
		return
	}
	current := l.current == ls
	before, beforeID := ls.status, ls.id
	switch ev.Type {
	case "init":
		ls.id = ev.SessionID
	case "commands":
		if current {
			l.commands = ev.Commands
		}
		if len(ev.Models) > 0 {
			l.claudeModels = ev.Models
		}
	case "done", "exit":
		ls.updated = time.Now()
		if ls.busy > 0 {
			ls.busy--
		}
		if ev.Type == "exit" {
			ls.busy = 0
		}
		if ls.busy == 0 {
			switch {
			case ev.IsError || ev.Type == "exit":
				ls.status = statusError
			case current && visible:
				ls.status = statusIdle
			default:
				ls.status = statusWaiting
			}
			// A finished background session needs no process.
			if !current {
				ls.agent = nil
				go a.Close()
			}
		}
	}
	changed := ls.status != before || ls.id != beforeID
	l.mu.Unlock()

	if l.app == nil {
		return
	}
	l.app.Event.Emit("claude", AgentEvent{Session: ls.key, Event: ev})
	if len(ev.Models) > 0 {
		l.app.Event.Emit("agents:changed")
	}
	if changed {
		l.app.Event.Emit("sessions:changed")
	}
}

// markSeen clears the waiting status of the session on screen, now that the
// window shows it.
func (l *Launcher) markSeen() {
	l.mu.Lock()
	ls := l.current
	seen := ls.status == statusWaiting
	if seen {
		ls.status = statusIdle
	}
	l.mu.Unlock()
	if seen {
		l.app.Event.Emit("sessions:changed")
	}
}

// startAgent prepares the current agent in the background: Claude starts
// warm so the first reply is faster and the slash command list arrives early.
func (l *Launcher) startAgent() {
	l.mu.Lock()
	ls := l.current
	a := ls.agent
	l.mu.Unlock()
	go func() {
		if err := a.Start(); err != nil {
			l.onAgentEvent(ls, a, session.Event{Type: "exit", Text: err.Error()})
		}
	}()
}

// leave steps away from the current session. One with a turn in progress
// keeps running in the background; any other is closed and forgotten (it is
// still on disk). Callers must hold l.mu.
func (l *Launcher) leave() {
	ls := l.current
	if ls == nil || ls.busy > 0 {
		return
	}
	if ls.agent != nil {
		ls.agent.Close()
		ls.agent = nil
	}
	if ls.id != "" {
		l.activity[ls.id] = ls.updated
	}
	l.live = slices.DeleteFunc(l.live, func(s *liveSession) bool { return s == ls })
}

// emitOpened tells the UI a session is now on screen.
func (l *Launcher) emitOpened(opened OpenedSession) {
	if l.app == nil {
		return
	}
	l.app.Event.Emit("session:open", opened)
	l.app.Event.Emit("sessions:changed")
	l.startAgent()
}

// Send sends a message to the current session, along with the output of any
// ! commands run since the last one.
func (l *Launcher) Send(text string) error {
	l.mu.Lock()
	ls := l.current
	a := ls.agent
	if ls.title == "" {
		ls.title = text
	}
	message := text
	if ls.shell != "" {
		message = ls.shell + "\n\n" + text
		ls.shell = ""
	}
	ls.busy++
	ls.status = statusRunning
	ls.updated = time.Now()
	l.mu.Unlock()
	l.app.Event.Emit("sessions:changed")

	err := a.Send(message)
	if err != nil {
		l.mu.Lock()
		ls.busy--
		ls.status = statusError
		l.mu.Unlock()
		l.app.Event.Emit("sessions:changed")
	}
	return err
}

// ShellResult is the output of a ! command, and why it failed if it did.
type ShellResult struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// RunShell runs a ! command in the working folder and returns its output.
// The command and output go to the agent with the next message, the way
// Claude Code's bash mode does.
func (l *Launcher) RunShell(command string) ShellResult {
	l.mu.Lock()
	ls, cwd := l.current, l.cfg.ResolvedCwd()
	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	path, args := config.Shell(command)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd
	cmd.Env = l.env
	session.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	res := ShellResult{Output: strings.TrimRight(tail(string(out), 32<<10), "\n")}
	switch {
	case ctx.Err() != nil:
		res.Error = "timed out after 5 minutes"
	case err != nil:
		res.Error = err.Error()
	}

	record := "<bash-input>" + command + "</bash-input>\n<bash-stdout>" + res.Output + "</bash-stdout>"
	if res.Error != "" {
		record += "\n<bash-stderr>" + res.Error + "</bash-stderr>"
	}
	l.mu.Lock()
	ls.shell = strings.TrimSpace(ls.shell + "\n" + record)
	l.mu.Unlock()
	return res
}

// tail returns the last n bytes of s.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// NewSession starts a fresh conversation. The one on screen keeps running in
// the background if it is mid-turn.
func (l *Launcher) NewSession() {
	l.mu.Lock()
	l.leave()
	ls := l.newLive("", "")
	l.mu.Unlock()
	l.emitOpened(OpenedSession{Key: ls.key})
}

// GetSessions lists the current agent's live and past sessions, newest
// first. The session on screen is always included.
func (l *Launcher) GetSessions() SessionList {
	l.mu.Lock()
	kind, cwd := l.cfg.Agent, l.cfg.ResolvedCwd()
	list := SessionList{Current: l.current.key, Sessions: []SessionItem{}}
	activity := maps.Clone(l.activity)
	liveIDs := map[string]bool{}
	for _, ls := range l.live {
		if ls.kind != kind && ls != l.current {
			continue
		}
		if ls.id != "" {
			liveIDs[ls.id] = true
		}
		list.Sessions = append(list.Sessions, SessionItem{
			Key: ls.key, ID: ls.id, Title: ls.title, Updated: ls.updated, Status: ls.status,
		})
	}
	l.mu.Unlock()

	var past []session.SessionInfo
	switch kind {
	case "claude":
		past = session.ClaudeSessions(cwd)
	case "codex":
		past = session.CodexSessions()
	}
	for _, s := range past {
		if !liveIDs[s.ID] {
			// Opening a session can touch its file; keep the time cmdcmd
			// last saw its status change so the order stays put.
			if t, ok := activity[s.ID]; ok {
				s.Updated = t
			}
			list.Sessions = append(list.Sessions, SessionItem{ID: s.ID, Title: s.Title, Updated: s.Updated, Status: statusIdle})
		}
	}
	slices.SortStableFunc(list.Sessions, func(a, b SessionItem) int { return b.Updated.Compare(a.Updated) })
	return list
}

// OpenSession puts a session on screen: a live one by key, or a past one by
// ID, which is resumed and its messages sent to the UI.
func (l *Launcher) OpenSession(item SessionItem) {
	l.mu.Lock()
	target := l.findLive(item)
	if target != nil && target == l.current {
		l.mu.Unlock()
		return
	}
	if target == nil && (item.ID == "" || l.cfg.Agent == "custom") {
		l.mu.Unlock()
		return
	}
	l.leave()

	if target != nil {
		l.current = target
		l.commands = nil
		if target.agent == nil {
			target.agent = l.newAgent(target, target.id)
		}
		if target.status == statusWaiting || target.status == statusError {
			target.status = statusIdle // seen
		}
		l.mu.Unlock()
		l.emitOpened(OpenedSession{Key: target.key})
		return
	}

	kind, cwd := l.cfg.Agent, l.cfg.ResolvedCwd()
	ls := l.newLive(item.ID, item.Title)
	ls.updated = item.Updated
	l.mu.Unlock()

	var history []session.HistoryItem
	if kind == "claude" {
		history = session.ClaudeHistory(cwd, item.ID)
	} else {
		history = session.CodexHistory(item.ID)
	}
	if history == nil {
		history = []session.HistoryItem{}
	}
	l.emitOpened(OpenedSession{Key: ls.key, History: history})
}

// findLive returns the live session an item refers to, if any. Callers must
// hold l.mu.
func (l *Launcher) findLive(item SessionItem) *liveSession {
	for _, ls := range l.live {
		if (item.Key != "" && ls.key == item.Key) || (item.ID != "" && ls.id == item.ID) {
			return ls
		}
	}
	return nil
}
