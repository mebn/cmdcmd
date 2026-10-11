package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"cmdcmd/internal/config"
	"cmdcmd/internal/session"
)

// FolderItem is one folder in the bar.
type FolderItem struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Pinned bool   `json:"pinned"`
	Exists bool   `json:"exists"`
	Status string `json:"status"` // the most pressing status of its live sessions
}

// FolderList is the folders in the bar, pinned first, the selected one (""
// for All) and the configured cwd, where sessions under All start.
type FolderList struct {
	Selected  string       `json:"selected"`
	Cwd       string       `json:"cwd"`
	Home      string       `json:"home"`
	AllStatus string       `json:"allStatus"`
	Folders   []FolderItem `json:"folders"`
}

// statusRank orders statuses by how much they need you.
var statusRank = map[string]int{statusIdle: 0, statusError: 1, statusWaiting: 2, statusRunning: 3}

// liveStatus is the most pressing status of the live sessions in a directory
// match accepts. Callers must hold l.mu.
func (l *Launcher) liveStatus(match func(cwd string) bool) string {
	status := statusIdle
	for _, ls := range l.live {
		if ls.kind == l.cfg.Agent && match(ls.cwd) && statusRank[ls.status] > statusRank[status] {
			status = ls.status
		}
	}
	return status
}

// scopeCwd is where a new session starts: the selected folder, or the
// configured cwd under All. Callers must hold l.mu.
func (l *Launcher) scopeCwd() string {
	if l.state.Selected != "" {
		return l.state.Selected
	}
	return filepath.Clean(l.cfg.ResolvedCwd())
}

// scopeDirs are the directories whose sessions are listed: the selected
// folder, or under All the configured cwd and every folder. Callers must
// hold l.mu.
func (l *Launcher) scopeDirs() []string {
	if l.state.Selected != "" {
		return []string{l.state.Selected}
	}
	return l.allDirs()
}

// allDirs are the configured cwd and every folder. Callers must hold l.mu.
func (l *Launcher) allDirs() []string {
	dirs := []string{filepath.Clean(l.cfg.ResolvedCwd())}
	for _, f := range l.state.Folders {
		if !slices.Contains(dirs, f.Path) {
			dirs = append(dirs, f.Path)
		}
	}
	return dirs
}

// GetFolders lists the folders in the bar, first adding any Claude Code has
// been used in.
func (l *Launcher) GetFolders() FolderList {
	l.discoverFolders()
	// Sessions run from Claude Code count toward a folder's status too.
	l.mu.Lock()
	dirs, kind := l.allDirs(), l.cfg.Agent
	l.mu.Unlock()
	busy := map[string]bool{}
	if kind == "claude" {
		for _, dir := range dirs {
			busy[dir] = session.ClaudeBusy(dir)
		}
	}
	status := func(match func(cwd string) bool) string {
		s := l.liveStatus(match)
		for dir, b := range busy {
			if b && match(dir) && statusRank[statusRunning] > statusRank[s] {
				s = statusRunning
			}
		}
		return s
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	home, _ := os.UserHomeDir()
	list := FolderList{
		Selected:  l.state.Selected,
		Cwd:       filepath.Clean(l.cfg.ResolvedCwd()),
		Home:      home,
		AllStatus: status(func(cwd string) bool { return slices.Contains(dirs, cwd) }),
		Folders:   []FolderItem{},
	}
	for _, pinned := range []bool{true, false} {
		for _, f := range l.state.Folders {
			if f.Pinned != pinned {
				continue
			}
			info, err := os.Stat(f.Path)
			list.Folders = append(list.Folders, FolderItem{
				Path: f.Path, Name: filepath.Base(f.Path), Pinned: f.Pinned, Exists: err == nil && info.IsDir(),
				Status: status(func(cwd string) bool { return cwd == f.Path }),
			})
		}
	}
	return list
}

// discoverFolders adds the directories Claude Code has run sessions in to
// the bar, newest first after the existing folders, skipping removed,
// missing and temporary ones.
func (l *Launcher) discoverFolders() {
	var found []string
	for _, p := range session.ClaudeProjects() {
		if info, err := os.Stat(p.Cwd); err == nil && info.IsDir() && !isTemp(p.Cwd) {
			found = append(found, p.Cwd)
		}
	}
	l.mu.Lock()
	added := false
	for _, path := range found {
		if !l.state.HasFolder(path) && !slices.Contains(l.state.Removed, path) {
			l.state.Folders = append(l.state.Folders, config.Folder{Path: path})
			added = true
		}
	}
	st := l.state
	l.mu.Unlock()
	if added {
		st.Save()
	}
}

// isTemp reports whether path is inside a temporary directory, like the
// scratch folders tools run Claude Code in.
func isTemp(path string) bool {
	for _, dir := range []string{os.TempDir(), "/tmp", "/private/tmp", "/var/folders", "/private/var/folders"} {
		if dir = filepath.Clean(dir); path == dir || strings.HasPrefix(path, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// changeState applies fn to the state under the lock, saves it, and tells
// the UI.
func (l *Launcher) changeState(fn func(s *config.State)) error {
	l.mu.Lock()
	fn(&l.state)
	st := l.state
	l.mu.Unlock()
	err := st.Save()
	if l.app != nil {
		l.app.Event.Emit("folders:changed")
		l.app.Event.Emit("sessions:changed")
	}
	return err
}

// SelectFolder shows the sessions of one folder, or of all of them for "".
// New sessions start there.
func (l *Launcher) SelectFolder(path string) error {
	return l.changeState(func(s *config.State) {
		if path == "" || s.HasFolder(path) {
			s.Selected = path
		}
	})
}

// AddFolder asks for a directory (which can also be created in the dialog),
// adds it to the bar and selects it. It returns the path, or "" if the
// dialog was cancelled.
func (l *Launcher) AddFolder() (string, error) {
	l.mu.Lock()
	start := l.scopeCwd()
	l.dialogOpen = true
	l.mu.Unlock()

	lowerWindow(l.window, true)
	activateApp()
	dialog := l.app.Dialog.OpenFile().
		SetTitle("New folder").
		SetMessage("Choose a folder for sessions, or create a new one.").
		SetButtonText("Add Folder").
		SetDirectory(start).
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true)
	if runtime.GOOS != "darwin" {
		// On macOS this would be a sheet hanging off the invisible stage.
		dialog.AttachToWindow(l.window)
	}
	path, err := dialog.PromptForSingleSelection()
	lowerWindow(l.window, false)

	l.mu.Lock()
	l.dialogOpen = false
	l.mu.Unlock()
	l.Show()
	if err != nil || path == "" {
		return "", err
	}

	path = filepath.Clean(path)
	return path, l.changeState(func(s *config.State) {
		if !s.HasFolder(path) {
			s.Folders = append(s.Folders, config.Folder{Path: path})
		}
		s.Removed = slices.DeleteFunc(s.Removed, func(p string) bool { return p == path })
		s.Selected = path
	})
}

// RemoveFolder takes a folder out of the bar. Nothing is deleted from disk.
func (l *Launcher) RemoveFolder(path string) error {
	return l.changeState(func(s *config.State) {
		s.Folders = slices.DeleteFunc(s.Folders, func(f config.Folder) bool { return f.Path == path })
		if !slices.Contains(s.Removed, path) {
			s.Removed = append(s.Removed, path)
		}
		if s.Selected == path {
			s.Selected = ""
		}
	})
}

// PinFolder pins or unpins a folder; pinned folders come first.
func (l *Launcher) PinFolder(path string, pinned bool) error {
	return l.changeState(func(s *config.State) {
		for i := range s.Folders {
			if s.Folders[i].Path == path {
				s.Folders[i].Pinned = pinned
			}
		}
	})
}

// ReorderFolders puts the folders in the order given. Folders missing from
// paths keep their place at the end.
func (l *Launcher) ReorderFolders(paths []string) error {
	return l.changeState(func(s *config.State) {
		rank := func(f config.Folder) int {
			if i := slices.Index(paths, f.Path); i >= 0 {
				return i
			}
			return len(paths)
		}
		slices.SortStableFunc(s.Folders, func(a, b config.Folder) int { return rank(a) - rank(b) })
	})
}

// PinSession pins or unpins a session; pinned sessions come first. Only a
// session the agent has given an ID can be pinned.
func (l *Launcher) PinSession(item SessionItem, pinned bool) error {
	id := item.ID
	if id == "" {
		l.mu.Lock()
		if ls := l.findLive(item); ls != nil {
			id = ls.id
		}
		l.mu.Unlock()
	}
	if id == "" {
		return nil
	}
	return l.changeState(func(s *config.State) {
		s.Pinned = slices.DeleteFunc(s.Pinned, func(p string) bool { return p == id })
		if pinned {
			s.Pinned = append(s.Pinned, id)
		}
	})
}

// DeleteSession stops a session if it is live and deletes its transcript.
// Deleting the session on screen starts a new one in its place.
func (l *Launcher) DeleteSession(item SessionItem) error {
	l.mu.Lock()
	ls := l.findLive(item)
	id, cwd, kind := item.ID, item.Cwd, l.cfg.Agent
	var opened *liveSession
	if ls != nil {
		if id == "" {
			id = ls.id
		}
		cwd, kind = ls.cwd, ls.kind
		if ls.agent != nil {
			ls.agent.Close()
			ls.agent = nil
		}
		l.live = slices.DeleteFunc(l.live, func(s *liveSession) bool { return s == ls })
		if l.current == ls {
			l.current = nil
			opened = l.newLive("", "", l.scopeCwd())
		}
	}
	l.mu.Unlock()

	var err error
	if id != "" {
		switch kind {
		case "claude":
			err = session.DeleteClaude(cwd, id)
		case "codex":
			err = session.DeleteCodex(id)
		}
		if os.IsNotExist(err) {
			err = nil
		}
	}
	if opened != nil {
		l.emitOpened(OpenedSession{Key: opened.key})
	}
	stateErr := l.changeState(func(s *config.State) {
		if id != "" {
			s.Pinned = slices.DeleteFunc(s.Pinned, func(p string) bool { return p == id })
			// Codex still lists a thread in its index after its file is gone.
			if !slices.Contains(s.Deleted, id) {
				s.Deleted = append(s.Deleted, id)
			}
		}
	})
	if err != nil {
		return err
	}
	return stateErr
}

// OpenFolder shows a folder in Finder, Explorer or the file manager.
func (l *Launcher) OpenFolder(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
