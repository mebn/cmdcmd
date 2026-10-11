package session

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// SessionInfo is a past conversation that can be resumed.
type SessionInfo struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
	Cwd     string    `json:"cwd"` // the directory it ran in
	Running bool      `json:"running"`
}

// HistoryItem is one entry of a past conversation, for showing it again.
type HistoryItem struct {
	Role string `json:"role"` // "user", "assistant" or "tool"
	Text string `json:"text"`
	Tool string `json:"tool,omitempty"`
}

const maxSessions = 30

// ---------- Claude Code ----------

// claudeProjectDir is where Claude Code keeps transcripts for sessions run in
// cwd: ~/.claude/projects/ plus cwd with every non-alphanumeric character
// replaced by "-".
func claudeProjectDir(cwd string) string {
	home, _ := os.UserHomeDir()
	slug := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(cwd, "-")
	return filepath.Join(home, ".claude", "projects", slug)
}

// ClaudeSessions lists the most recent Claude Code sessions run in cwd,
// newest first, with their titles (see scanMeta) and whether a turn is in
// progress.
func ClaudeSessions(cwd string) []SessionInfo {
	dir := claudeProjectDir(cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var all []SessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		all = append(all, SessionInfo{ID: strings.TrimSuffix(e.Name(), ".jsonl"), Updated: info.ModTime()})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Updated.After(all[j].Updated) })

	var out []SessionInfo
	for _, s := range all {
		if len(out) == maxSessions {
			break
		}
		if m := readMeta(filepath.Join(dir, s.ID+".jsonl")); m.title != "" {
			s.Title, s.Running, s.Cwd = m.title, m.running, cwd
			out = append(out, s)
		}
	}
	return out
}

// claudeMeta is what the bar shows about a transcript: its title and
// whether a turn is in progress.
type claudeMeta struct {
	mod     time.Time
	title   string
	running bool
}

// metas caches claudeMeta by path; a transcript is read again only when it
// changes. The bar lists sessions often, from many folders.
var metas = struct {
	sync.Mutex
	m map[string]claudeMeta
}{m: map[string]claudeMeta{}}

// idleAfter is how long a transcript can go unwritten before a turn that
// seems to be in progress counts as abandoned (Claude Code quit mid-turn).
const idleAfter = 10 * time.Minute

// readMeta returns a transcript's title and status, from the cache when the
// file hasn't changed.
func readMeta(path string) claudeMeta {
	info, err := os.Stat(path)
	if err != nil {
		return claudeMeta{}
	}
	metas.Lock()
	m, ok := metas.m[path]
	metas.Unlock()
	if !ok || !m.mod.Equal(info.ModTime()) {
		m = scanMeta(path)
		m.mod = info.ModTime()
		metas.Lock()
		metas.m[path] = m
		metas.Unlock()
	}
	if time.Since(m.mod) > idleAfter {
		m.running = false
	}
	return m
}

var (
	customTitleType = []byte(`"type":"custom-title"`)
	aiTitleType     = []byte(`"type":"ai-title"`)
	userType        = []byte(`"type":"user"`)
	assistantType   = []byte(`"type":"assistant"`)
)

// scanMeta reads a transcript once. The title is the newest name given with
// /rename (or by cmdcmd), else Claude Code's generated title, else the first
// message, the way Claude Code's own session list picks it. A turn is in
// progress when the last message is a prompt, a tool result, or a reply that
// called a tool.
func scanMeta(path string) claudeMeta {
	var custom, ai, first string
	var last []byte
	eachLine(path, func(line []byte) bool {
		switch {
		case bytes.Contains(line, customTitleType):
			var r struct {
				Title string `json:"customTitle"`
			}
			if json.Unmarshal(line, &r) == nil {
				custom = r.Title
			}
		case bytes.Contains(line, aiTitleType):
			var r struct {
				Title string `json:"aiTitle"`
			}
			if json.Unmarshal(line, &r) == nil {
				ai = r.Title
			}
		case bytes.Contains(line, userType) || bytes.Contains(line, assistantType):
			if first == "" {
				if items := claudeItems(line); len(items) > 0 && items[0].Role == "user" {
					first = items[0].Text
				}
			}
			last = append(last[:0], line...)
		}
		return true
	})

	m := claudeMeta{title: truncate(cmp.Or(custom, ai, first), 80)}
	var r struct {
		Type        string `json:"type"`
		IsMeta      bool   `json:"isMeta"`
		IsSidechain bool   `json:"isSidechain"`
		Message     struct {
			StopReason string          `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(last, &r) == nil {
		switch r.Type {
		case "user":
			// An interrupted turn ends with a note to that effect.
			m.running = !bytes.Contains(r.Message.Content, []byte("[Request interrupted"))
		case "assistant":
			m.running = r.Message.StopReason == "tool_use"
		}
	}
	return m
}

// ClaudeRunning reports whether a turn is in progress in a Claude Code
// session run in cwd, judging by its transcript.
func ClaudeRunning(cwd, id string) bool {
	return readMeta(filepath.Join(claudeProjectDir(cwd), id+".jsonl")).running
}

// ClaudeTitle returns a Claude Code session's title, "" if it has none yet.
func ClaudeTitle(cwd, id string) string {
	return readMeta(filepath.Join(claudeProjectDir(cwd), id+".jsonl")).title
}

// ClaudeBusy reports whether any session run in cwd has a turn in progress.
// Only transcripts written recently are read.
func ClaudeBusy(cwd string) bool {
	dir := claudeProjectDir(cwd)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) < idleAfter &&
			readMeta(filepath.Join(dir, e.Name())).running {
			return true
		}
	}
	return false
}

// RenameClaude names a Claude Code session the way /rename does, by adding a
// custom-title record to its transcript; the newest one wins.
func RenameClaude(cwd, id, title string) error {
	return appendLine(filepath.Join(claudeProjectDir(cwd), id+".jsonl"), map[string]string{
		"type": "custom-title", "customTitle": title, "sessionId": id,
	})
}

// RenameCodex names a Codex thread by adding an entry to its session index;
// later entries win.
func RenameCodex(id, title string) error {
	home, _ := os.UserHomeDir()
	return appendLine(filepath.Join(home, ".codex", "session_index.jsonl"), map[string]string{
		"id": id, "thread_name": title, "updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// appendLine adds one JSON line to an existing file.
func appendLine(path string, record any) error {
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// DeleteClaude deletes the transcript of a Claude Code session run in cwd.
func DeleteClaude(cwd, id string) error {
	return os.Remove(filepath.Join(claudeProjectDir(cwd), id+".jsonl"))
}

// ClaudeHistory returns the messages of a Claude Code session run in cwd,
// and how far into the transcript they reach, for ClaudeTail to go on from.
func ClaudeHistory(cwd, id string) ([]HistoryItem, int64) {
	var out []HistoryItem
	var offset int64
	eachLine(filepath.Join(claudeProjectDir(cwd), id+".jsonl"), func(line []byte) bool {
		if line[len(line)-1] != '\n' {
			return false // still being written; ClaudeTail picks it up
		}
		offset += int64(len(line))
		out = appendMerged(out, claudeItems(line)...)
		return true
	})
	return out, offset
}

// ClaudeSize returns the length of a session's transcript.
func ClaudeSize(cwd, id string) int64 {
	info, err := os.Stat(filepath.Join(claudeProjectDir(cwd), id+".jsonl"))
	if err != nil {
		return 0
	}
	return info.Size()
}

// cmdcmdEntrypoint marks transcript records written by claude -p, which is
// how cmdcmd runs it.
const cmdcmdEntrypoint = "sdk-cli"

// ClaudeTail reads the complete lines of a session's transcript from offset
// on. It returns the messages in them that another client wrote (Claude
// Code in a terminal or an IDE) and the offset to go on from. Records from
// claude -p are skipped: cmdcmd already showed those as they streamed.
func ClaudeTail(cwd, id string, offset int64) ([]HistoryItem, int64) {
	f, err := os.Open(filepath.Join(claudeProjectDir(cwd), id+".jsonl"))
	if err != nil {
		return nil, offset
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, offset
	}
	if info.Size() <= offset {
		// Unchanged, or rewritten shorter: go on from its end.
		return nil, info.Size()
	}
	buf := make([]byte, info.Size()-offset)
	n, _ := f.ReadAt(buf, offset)
	buf = buf[:n]
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return nil, offset
	}
	var out []HistoryItem
	for _, line := range bytes.Split(buf[:end], []byte("\n")) {
		var r struct {
			Entrypoint string `json:"entrypoint"`
		}
		if json.Unmarshal(line, &r) != nil || r.Entrypoint == cmdcmdEntrypoint {
			continue
		}
		out = append(out, claudeItems(line)...)
	}
	return out, offset + int64(end) + 1
}

// ClaudeProject is a directory Claude Code has run sessions in.
type ClaudeProject struct {
	Cwd     string
	Updated time.Time // when its newest session last changed
}

// projectCwds caches the directory each ~/.claude/projects folder stands
// for; the folder name alone is ambiguous, so it is read from a transcript.
var projectCwds = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// ClaudeProjects lists the directories Claude Code has run sessions in,
// most recently used first.
func ClaudeProjects() []ClaudeProject {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".claude", "projects")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []ClaudeProject
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(root, d.Name())
		entries, _ := os.ReadDir(dir)
		var newest string
		var updated time.Time
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().After(updated) {
				newest, updated = filepath.Join(dir, e.Name()), info.ModTime()
			}
		}
		if newest == "" {
			continue
		}
		projectCwds.Lock()
		cwd, ok := projectCwds.m[d.Name()]
		projectCwds.Unlock()
		if !ok {
			cwd = transcriptCwd(newest)
			projectCwds.Lock()
			projectCwds.m[d.Name()] = cwd
			projectCwds.Unlock()
		}
		if cwd != "" {
			out = append(out, ClaudeProject{Cwd: cwd, Updated: updated})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// transcriptCwd returns the directory a transcript's session ran in, from
// the first record that names it.
func transcriptCwd(path string) string {
	var cwd string
	eachLine(path, func(line []byte) bool {
		var r struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(line, &r) == nil && r.Cwd != "" {
			cwd = filepath.Clean(r.Cwd)
			return false
		}
		return true
	})
	return cwd
}

type claudeRecord struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

var (
	commandTag = regexp.MustCompile(`<command-(name|args)>([\s\S]*?)</command-(?:name|args)>`)
	// The output of ! commands, sent along with the next message.
	bashTag = regexp.MustCompile(`<bash-(input|stdout|stderr)>[\s\S]*?</bash-(?:input|stdout|stderr)>`)
)

// claudeItems turns one transcript record into history items.
func claudeItems(line []byte) []HistoryItem {
	var r claudeRecord
	if json.Unmarshal(line, &r) != nil || r.IsMeta || r.IsSidechain {
		return nil
	}
	switch r.Type {
	case "user":
		var text string
		if json.Unmarshal(r.Message.Content, &text) != nil {
			for _, b := range blocks(r.Message.Content) {
				if b.Type == "text" {
					text += textOf(b.Content) + b.Text
				}
			}
		}
		if text = userText(text); text != "" {
			return []HistoryItem{{Role: "user", Text: text}}
		}
	case "assistant":
		var items []HistoryItem
		for _, b := range blocks(r.Message.Content) {
			switch b.Type {
			case "text":
				items = append(items, HistoryItem{Role: "assistant", Text: b.Text})
			case "tool_use":
				items = append(items, HistoryItem{Role: "tool", Tool: b.Name, Text: summarize(b.Input)})
			}
		}
		return items
	}
	return nil
}

// userText cleans up a user message: slash command invocations become
// "/name args", ! command output is removed, and other tool-generated
// messages (which start with a tag) are dropped.
func userText(text string) string {
	text = strings.TrimSpace(bashTag.ReplaceAllString(text, ""))
	if strings.Contains(text, "<command-name>") {
		var name, args string
		for _, m := range commandTag.FindAllStringSubmatch(text, -1) {
			if m[1] == "name" {
				name = strings.TrimSpace(m[2])
			} else {
				args = strings.TrimSpace(m[2])
			}
		}
		return strings.TrimSpace("/" + strings.TrimPrefix(name, "/") + " " + args)
	}
	if strings.HasPrefix(text, "<") {
		return ""
	}
	return text
}

// ---------- Codex ----------

// CodexSessions lists the most recent Codex threads that ran in a directory
// match accepts, newest first, from ~/.codex/session_index.jsonl. The
// directory comes from the start of each thread's rollout file.
func CodexSessions(match func(cwd string) bool) []SessionInfo {
	home, _ := os.UserHomeDir()
	byID := map[string]SessionInfo{}
	eachLine(filepath.Join(home, ".codex", "session_index.jsonl"), func(line []byte) bool {
		var e struct {
			ID      string    `json:"id"`
			Name    string    `json:"thread_name"`
			Updated time.Time `json:"updated_at"`
		}
		if json.Unmarshal(line, &e) == nil && e.ID != "" && e.Name != "" {
			// Later lines update earlier ones.
			byID[e.ID] = SessionInfo{ID: e.ID, Title: truncate(e.Name, 80), Updated: e.Updated}
		}
		return true
	})
	all := make([]SessionInfo, 0, len(byID))
	for _, s := range byID {
		all = append(all, s)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Updated.After(all[j].Updated) })

	paths := codexRollouts()
	var out []SessionInfo
	for _, s := range all {
		if len(out) == maxSessions {
			break
		}
		if s.Cwd = codexCwd(paths[s.ID]); s.Cwd != "" && match(s.Cwd) {
			out = append(out, s)
		}
	}
	return out
}

// codexRollouts maps thread IDs to their rollout files under ~/.codex/sessions.
func codexRollouts() map[string]string {
	home, _ := os.UserHomeDir()
	paths := map[string]string{}
	filepath.WalkDir(filepath.Join(home, ".codex", "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			// rollout-<timestamp>-<id>.jsonl; the ID is the last 36 characters.
			name := strings.TrimSuffix(d.Name(), ".jsonl")
			if len(name) >= 36 {
				paths[name[len(name)-36:]] = p
			}
		}
		return nil
	})
	return paths
}

// codexCwd returns the directory a rollout's thread ran in, from its first
// line.
func codexCwd(path string) string {
	var cwd string
	if path == "" {
		return ""
	}
	eachLine(path, func(line []byte) bool {
		var r struct {
			Type    string `json:"type"`
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &r) == nil && r.Type == "session_meta" {
			cwd = filepath.Clean(r.Payload.Cwd)
		}
		return false
	})
	return cwd
}

// DeleteCodex deletes a Codex thread's rollout file.
func DeleteCodex(id string) error {
	path := codexRollouts()[id]
	if path == "" {
		return os.ErrNotExist
	}
	return os.Remove(path)
}

// CodexHistory returns the messages of a Codex thread from its rollout file
// under ~/.codex/sessions.
func CodexHistory(id string) []HistoryItem {
	path := codexRollouts()[id]
	if path == "" {
		return nil
	}
	var out []HistoryItem
	eachLine(path, func(line []byte) bool {
		var r struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Name    string `json:"name"`
				Input   string `json:"input"`
				Args    string `json:"arguments"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &r) != nil || r.Type != "response_item" {
			return true
		}
		p := r.Payload
		switch p.Type {
		case "message":
			for _, c := range p.Content {
				switch {
				case p.Role == "user" && c.Type == "input_text":
					if text := userText(c.Text); text != "" {
						out = appendMerged(out, HistoryItem{Role: "user", Text: text})
					}
				case p.Role == "assistant" && c.Type == "output_text":
					out = appendMerged(out, HistoryItem{Role: "assistant", Text: c.Text})
				}
			}
		case "function_call", "custom_tool_call":
			out = append(out, HistoryItem{Role: "tool", Tool: p.Name, Text: truncate(p.Input+p.Args, 120)})
		}
		return true
	})
	return out
}

// ---------- helpers ----------

// appendMerged appends items, joining consecutive assistant texts into one
// message the way they were shown while streaming.
func appendMerged(out []HistoryItem, items ...HistoryItem) []HistoryItem {
	for _, it := range items {
		if n := len(out); n > 0 && it.Role == "assistant" && out[n-1].Role == "assistant" {
			out[n-1].Text += "\n\n" + it.Text
			continue
		}
		out = append(out, it)
	}
	return out
}

// eachLine calls fn for each line of a file until fn returns false.
func eachLine(path string, fn func([]byte) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && !fn(line) {
			return
		}
		if err != nil {
			return
		}
	}
}

// Fingerprint changes whenever a session of the given agent in one of dirs
// is added, removed or written to.
func Fingerprint(kind string, dirs []string) uint64 {
	h := fnv.New64a()
	add := func(path string) {
		if info, err := os.Stat(path); err == nil {
			fmt.Fprint(h, path, info.Size(), info.ModTime().UnixNano())
		}
	}
	switch kind {
	case "claude":
		for _, cwd := range dirs {
			dir := claudeProjectDir(cwd)
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".jsonl") {
					add(filepath.Join(dir, e.Name()))
				}
			}
		}
	case "codex":
		home, _ := os.UserHomeDir()
		add(filepath.Join(home, ".codex", "session_index.jsonl"))
	}
	return h.Sum64() | 1 // never 0, which means "not taken yet"
}
