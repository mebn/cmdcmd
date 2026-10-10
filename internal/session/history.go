package session

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SessionInfo is a past conversation that can be resumed.
type SessionInfo struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
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
// newest first. A session's title is its first message.
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
		if s.Title = claudeTitle(filepath.Join(dir, s.ID+".jsonl")); s.Title != "" {
			out = append(out, s)
		}
	}
	return out
}

// claudeTitle returns the first user message in a transcript, reading only
// as far as needed.
func claudeTitle(path string) string {
	var title string
	eachLine(path, func(line []byte) bool {
		if items := claudeItems(line); len(items) > 0 && items[0].Role == "user" {
			title = truncate(items[0].Text, 80)
			return false
		}
		return true
	})
	return title
}

// ClaudeHistory returns the messages of a Claude Code session run in cwd.
func ClaudeHistory(cwd, id string) []HistoryItem {
	var out []HistoryItem
	eachLine(filepath.Join(claudeProjectDir(cwd), id+".jsonl"), func(line []byte) bool {
		out = appendMerged(out, claudeItems(line)...)
		return true
	})
	return out
}

type claudeRecord struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

var commandTag = regexp.MustCompile(`<command-(name|args)>([\s\S]*?)</command-(?:name|args)>`)

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
// "/name args", and other tool-generated messages (which start with a tag)
// are dropped.
func userText(text string) string {
	text = strings.TrimSpace(text)
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

// CodexSessions lists the most recent Codex threads, newest first, from
// ~/.codex/session_index.jsonl.
func CodexSessions() []SessionInfo {
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
	out := make([]SessionInfo, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if len(out) > maxSessions {
		out = out[:maxSessions]
	}
	return out
}

// CodexHistory returns the messages of a Codex thread from its rollout file
// under ~/.codex/sessions.
func CodexHistory(id string) []HistoryItem {
	home, _ := os.UserHomeDir()
	var path string
	filepath.WalkDir(filepath.Join(home, ".codex", "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, id+".jsonl") {
			path = p
			return fs.SkipAll
		}
		return nil
	})
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
