package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"runtime"
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
	cwd     string        // the directory the agent runs in
	title   string
	updated time.Time // when the status last changed; the bar sorts by it
	status  string
	busy    int    // turns sent but not finished
	shell   string // output of ! commands, sent along with the next message

	// The transcript is followed while the session is on screen, to show
	// turns another client (Claude Code in a terminal) adds to it.
	tail    int64 // how far it has been read
	tailing bool  // tail is set
	stale   bool  // another client added turns the agent hasn't loaded
}

// AppendedItems are messages another client added to a session.
type AppendedItems struct {
	Key   string                `json:"key"`
	Items []session.HistoryItem `json:"items"`
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
	Cwd     string    `json:"cwd"`
	Pinned  bool      `json:"pinned"`
}

// SessionList is the current agent's sessions, newest first, and the key of
// the one on screen.
type SessionList struct {
	Current    string        `json:"current"`
	CurrentCwd string        `json:"currentCwd"` // where the session on screen runs
	Sessions   []SessionItem `json:"sessions"`
}

// newLive creates a session for the selected agent in cwd, continuing the
// session with the given ID if it is not empty, and makes it current.
// Callers must hold l.mu.
func (l *Launcher) newLive(resumeID, title, cwd string) *liveSession {
	l.nextKey++
	ls := &liveSession{
		key:     fmt.Sprint("s", l.nextKey),
		kind:    l.cfg.Agent,
		id:      resumeID,
		cwd:     cwd,
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
		Cwd:      ls.cwd,
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

// Send sends a message and any attached files (paths) to the current
// session, along with the output of any ! commands run since the last one.
func (l *Launcher) Send(text string, files []string) error {
	l.mu.Lock()
	ls := l.current
	if ls.stale && ls.busy == 0 && ls.id != "" {
		// Resume afresh so the agent sees the turns another client added.
		go ls.agent.Close()
		ls.agent = l.newAgent(ls, ls.id)
		ls.stale = false
	}
	a := ls.agent
	if ls.title == "" {
		ls.title = text
		if text == "" && len(files) > 0 {
			ls.title = filepath.Base(files[0])
		}
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

	err := a.Send(message, files)
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
	ls := l.current
	cwd := ls.cwd
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

// NewSession starts a fresh conversation in the selected folder, or in the
// configured cwd when All is selected. The one on screen keeps running in
// the background if it is mid-turn.
func (l *Launcher) NewSession() {
	l.mu.Lock()
	l.leave()
	ls := l.newLive("", "", l.scopeCwd())
	l.mu.Unlock()
	l.emitOpened(OpenedSession{Key: ls.key})
}

// GetSessions lists the current agent's live and past sessions in the
// selected folder (or every folder, for All), pinned first, then newest
// first.
func (l *Launcher) GetSessions() SessionList {
	l.mu.Lock()
	kind, dirs := l.cfg.Agent, l.scopeDirs()
	inScope := func(cwd string) bool { return slices.Contains(dirs, filepath.Clean(cwd)) }
	pinned := map[string]bool{}
	for _, id := range l.state.Pinned {
		pinned[id] = true
	}
	deleted := map[string]bool{}
	for _, id := range l.state.Deleted {
		deleted[id] = true
	}
	list := SessionList{Current: l.current.key, CurrentCwd: l.current.cwd, Sessions: []SessionItem{}}
	activity := maps.Clone(l.activity)
	liveIDs := map[string]bool{}
	for _, ls := range l.live {
		if (ls.kind != kind && ls != l.current) || !inScope(ls.cwd) {
			continue
		}
		if ls.id != "" {
			liveIDs[ls.id] = true
		}
		list.Sessions = append(list.Sessions, SessionItem{
			Key: ls.key, ID: ls.id, Title: ls.title, Updated: ls.updated, Status: ls.status,
			Cwd: ls.cwd, Pinned: pinned[ls.id],
		})
	}
	l.mu.Unlock()

	var past []session.SessionInfo
	switch kind {
	case "claude":
		for _, dir := range dirs {
			past = append(past, session.ClaudeSessions(dir)...)
		}
	case "codex":
		past = session.CodexSessions(inScope)
	}
	for _, s := range past {
		if liveIDs[s.ID] || deleted[s.ID] {
			continue
		}
		// Opening a session can touch its file; keep the time cmdcmd last
		// saw its status change so the order stays put.
		if t, ok := activity[s.ID]; ok {
			s.Updated = t
		}
		list.Sessions = append(list.Sessions, SessionItem{
			ID: s.ID, Title: s.Title, Updated: s.Updated, Status: statusFor(s.Running), Cwd: s.Cwd, Pinned: pinned[s.ID],
		})
	}
	// A live Claude session can also be renamed or have turns run from Claude
	// Code; its transcript tells.
	if kind == "claude" {
		for i, s := range list.Sessions {
			if s.Key == "" || s.ID == "" {
				continue
			}
			if t := session.ClaudeTitle(s.Cwd, s.ID); t != "" {
				list.Sessions[i].Title = t
			}
			if s.Status == statusIdle && session.ClaudeRunning(s.Cwd, s.ID) {
				list.Sessions[i].Status = statusRunning
			}
		}
	}
	slices.SortStableFunc(list.Sessions, func(a, b SessionItem) int {
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		return b.Updated.Compare(a.Updated)
	})
	// All can span every folder on the machine; keep the bar to the recent
	// ones (and everything pinned or live).
	if len(list.Sessions) > maxListed {
		kept := list.Sessions[:0]
		for i, s := range list.Sessions {
			if i < maxListed || s.Pinned || s.Key != "" {
				kept = append(kept, s)
			}
		}
		list.Sessions = kept
	}
	return list
}

const maxListed = 60

func statusFor(running bool) string {
	if running {
		return statusRunning
	}
	return statusIdle
}

// RenameSession names a session. A Claude session's name is written to its
// transcript the way /rename does, so Claude Code shows it too; a Codex
// thread's goes to Codex's session index.
func (l *Launcher) RenameSession(item SessionItem, title string) error {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return nil
	}
	if r := []rune(title); len(r) > maxTitle {
		title = string(r[:maxTitle])
	}
	l.mu.Lock()
	id, cwd, kind := item.ID, item.Cwd, l.cfg.Agent
	if ls := l.findLive(item); ls != nil {
		ls.title = title
		id, cwd, kind = cmp.Or(id, ls.id), ls.cwd, ls.kind
	}
	l.mu.Unlock()

	var err error
	if id != "" {
		switch kind {
		case "claude":
			err = session.RenameClaude(cwd, id, title)
		case "codex":
			err = session.RenameCodex(id, title)
		}
	}
	l.app.Event.Emit("sessions:changed")
	return err
}

// maxTitle caps session names, in characters.
const maxTitle = 100

// watchSessions reloads the bar while the window is up whenever a session
// changes on disk: Claude Code started, renamed or finished one.
func (l *Launcher) watchSessions() {
	var last uint64
	for range time.Tick(2 * time.Second) {
		if l.window == nil || !l.window.IsVisible() {
			continue
		}
		l.mu.Lock()
		dirs, kind := l.allDirs(), l.cfg.Agent
		l.mu.Unlock()
		if fp := session.Fingerprint(kind, dirs); fp != last {
			if last != 0 {
				l.app.Event.Emit("sessions:changed")
			}
			last = fp
		}
	}
}

// followTranscripts shows, live, turns that another client such as Claude
// Code in a terminal adds to the session on screen, and marks the session
// so its agent picks them up before the next message.
func (l *Launcher) followTranscripts() {
	for range time.Tick(700 * time.Millisecond) {
		l.mu.Lock()
		ls := l.current
		if ls == nil || ls.kind != "claude" || ls.id == "" {
			l.mu.Unlock()
			continue
		}
		id, cwd, offset, tailing := ls.id, ls.cwd, ls.tail, ls.tailing
		l.mu.Unlock()

		if !tailing {
			// A new session: everything in it so far came from cmdcmd.
			size := session.ClaudeSize(cwd, id)
			l.mu.Lock()
			if ls.id == id && !ls.tailing {
				ls.tail, ls.tailing = size, true
			}
			l.mu.Unlock()
			continue
		}
		items, next := session.ClaudeTail(cwd, id, offset)
		if next == offset {
			continue
		}
		l.mu.Lock()
		if ls.tail != offset || ls.id != id {
			l.mu.Unlock()
			continue
		}
		ls.tail = next
		if len(items) > 0 {
			ls.stale = true
			ls.updated = time.Now()
		}
		l.mu.Unlock()
		if len(items) > 0 {
			l.app.Event.Emit("session:append", AppendedItems{Key: ls.key, Items: items})
		}
	}
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

	kind, cwd := l.cfg.Agent, item.Cwd
	if cwd == "" {
		cwd = l.scopeCwd()
	}
	ls := l.newLive(item.ID, item.Title, cwd)
	ls.updated = item.Updated
	l.mu.Unlock()

	var history []session.HistoryItem
	if kind == "claude" {
		var offset int64
		history, offset = session.ClaudeHistory(cwd, item.ID)
		l.mu.Lock()
		ls.tail, ls.tailing = offset, true
		l.mu.Unlock()
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

// CopyResumeCommand puts the command that continues a session in a terminal
// on the clipboard, e.g. cd ~/code/app && claude --resume <id>. Claude Code
// finds a session only from the folder it ran in, hence the cd.
func (l *Launcher) CopyResumeCommand(item SessionItem) error {
	l.mu.Lock()
	id, cwd, kind := item.ID, item.Cwd, l.cfg.Agent
	if ls := l.findLive(item); ls != nil {
		id, cwd, kind = cmp.Or(id, ls.id), ls.cwd, ls.kind
	}
	l.mu.Unlock()

	var command string
	switch {
	case id == "":
		return errors.New("this session has no ID yet; send a message first")
	case kind == "codex":
		command = "codex resume " + id
	case kind == "claude":
		command = "claude --resume " + id
		if cwd != "" {
			command = "cd " + shellQuote(cwd) + " && " + command
		}
	default:
		return errors.New("this agent's sessions can't be resumed")
	}
	if !l.app.Clipboard.SetText(command) {
		return errors.New("couldn't copy to the clipboard")
	}
	return nil
}

// shellQuote quotes a path for the system shell when it needs it.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " '\"$`\\!&;|<>()*?[]{}#~") {
		return s
	}
	if runtime.GOOS == "windows" {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
