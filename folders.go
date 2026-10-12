package main

import (
	"cmp"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"cmdcmd/internal/config"
	"cmdcmd/internal/session"
	"cmdcmd/internal/wsl"
)

// FolderItem is one folder in the bar.
type FolderItem struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Pinned bool   `json:"pinned"`
	Exists bool   `json:"exists"`
	Status string `json:"status"` // the most pressing status of its live sessions
	WSL    bool   `json:"wsl"`    // the folder is in WSL rather than on Windows
}

// FolderList is the folders in the bar, pinned first, the selected one (""
// for All) and the configured cwd, where sessions under All start.
type FolderList struct {
	Selected  string       `json:"selected"`
	Cwd       string       `json:"cwd"`
	Home      string       `json:"home"`
	WSLHome   string       `json:"wslHome"` // the home folder in WSL, when it is used
	AllStatus string       `json:"allStatus"`
	Folders   []FolderItem `json:"folders"`
	// Mixed is set when folders and sessions from both Windows and WSL are
	// shown, so each says which side it is on; WSLName names the distro.
	Mixed   bool   `json:"mixed"`
	WSLName string `json:"wslName"`
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

// shows reports whether a directory is on a side (Windows or WSL) the
// environment setting shows. Callers must hold l.mu.
func (l *Launcher) shows(dir string) bool {
	if wsl.IsPath(dir) {
		return l.cfg.UsesWSL()
	}
	return l.cfg.UsesWindows()
}

// selected is the selected folder, or "" for All, also when the selected
// folder is on a side that isn't shown. Callers must hold l.mu.
func (l *Launcher) selected() string {
	if l.state.Selected != "" && l.shows(l.state.Selected) {
		return l.state.Selected
	}
	return ""
}

// defaultCwd is the configured cwd, where sessions under All start. With
// only WSL in use it is resolved in WSL: "~" is the home folder there and a
// Windows path is reached through /mnt. Callers must hold l.mu.
func (l *Launcher) defaultCwd() string {
	if l.cfg.UsesWindows() {
		return filepath.Clean(l.cfg.ResolvedCwd())
	}
	home := wsl.Home()
	switch cwd := l.cfg.Cwd; {
	case cwd == "" || cwd == "~":
		return home
	case strings.HasPrefix(cwd, "~/"):
		return path.Join(home, cwd[2:])
	case strings.HasPrefix(cwd, "/"):
		return path.Clean(cwd)
	default:
		return cmp.Or(wsl.FromWindows(cwd), home)
	}
}

// scopeCwd is where a new session starts: the selected folder, or the
// configured cwd under All. Callers must hold l.mu.
func (l *Launcher) scopeCwd() string {
	if sel := l.selected(); sel != "" {
		return sel
	}
	return l.defaultCwd()
}

// scopeDirs are the directories whose sessions are listed: the selected
// folder, or under All the configured cwd and every folder. Callers must
// hold l.mu.
func (l *Launcher) scopeDirs() []string {
	if sel := l.selected(); sel != "" {
		return []string{sel}
	}
	return l.allDirs()
}

// allDirs are the configured cwd and every folder on a side that is shown.
// Callers must hold l.mu.
func (l *Launcher) allDirs() []string {
	dirs := []string{l.defaultCwd()}
	for _, f := range l.state.Folders {
		if l.shows(f.Path) && !slices.Contains(dirs, f.Path) {
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
		Selected:  l.selected(),
		Cwd:       l.defaultCwd(),
		Home:      home,
		AllStatus: status(func(cwd string) bool { return slices.Contains(dirs, cwd) }),
		Folders:   []FolderItem{},
		Mixed:     l.cfg.UsesWindows() && l.cfg.UsesWSL(),
	}
	if l.cfg.UsesWSL() {
		list.WSLHome, list.WSLName = wsl.Home(), wsl.Distro()
	}
	for _, pinned := range []bool{true, false} {
		for _, f := range l.state.Folders {
			if f.Pinned != pinned || !l.shows(f.Path) {
				continue
			}
			info, err := os.Stat(wsl.Native(f.Path))
			list.Folders = append(list.Folders, FolderItem{
				Path: f.Path, Name: filepath.Base(f.Path), Pinned: f.Pinned, Exists: err == nil && info.IsDir(),
				Status: status(func(cwd string) bool { return cwd == f.Path }),
				WSL:    wsl.IsPath(f.Path),
			})
		}
	}
	return list
}

// discoverFolders adds the directories Claude Code has run sessions in, on
// Windows and in WSL as the environment setting says, to the bar, newest
// first after the existing folders, skipping removed, missing and temporary
// ones.
func (l *Launcher) discoverFolders() {
	l.mu.Lock()
	var sides []bool // in WSL or not
	if l.cfg.UsesWindows() {
		sides = append(sides, false)
	}
	if l.cfg.UsesWSL() {
		sides = append(sides, true)
	}
	l.mu.Unlock()

	var projects []session.ClaudeProject
	for _, inWSL := range sides {
		projects = append(projects, session.ClaudeProjects(inWSL)...)
	}
	slices.SortStableFunc(projects, func(a, b session.ClaudeProject) int { return b.Updated.Compare(a.Updated) })
	var found []string
	for _, p := range projects {
		if info, err := os.Stat(wsl.Native(p.Cwd)); err == nil && info.IsDir() && !isTemp(p.Cwd) {
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
func isTemp(dir string) bool {
	if wsl.IsPath(dir) {
		return dir == "/tmp" || strings.HasPrefix(dir, "/tmp/")
	}
	for _, tmp := range []string{os.TempDir(), "/tmp", "/private/tmp", "/var/folders", "/private/var/folders"} {
		if tmp = filepath.Clean(tmp); dir == tmp || strings.HasPrefix(dir, tmp+string(filepath.Separator)) {
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
	start := wsl.Native(l.scopeCwd())
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
	// A folder in WSL's share is kept as its Linux path, so it runs in WSL;
	// with only WSL in use, so is a Windows folder (through /mnt).
	l.mu.Lock()
	if l.cfg.UsesWSL() && (wsl.IsShare(path) || !l.cfg.UsesWindows()) {
		if p := wsl.FromWindows(path); p != "" {
			path = p
		}
	}
	l.mu.Unlock()
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
		cmd = exec.Command("explorer", wsl.Native(path))
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
