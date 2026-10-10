package session

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// codex_turn.jsonl is a real `codex exec --json` transcript: a shell command
// followed by a reply.
func TestCodexHandleTurn(t *testing.T) {
	f, err := os.Open("testdata/codex_turn.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var events []Event
	c := NewCodex(Options{}, func(ev Event) { events = append(events, ev) })
	finished := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if c.handle(sc.Bytes()) {
			finished = true
		}
	}

	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	if got, want := strings.Join(types, ","), "init,tool,block,text,done"; got != want {
		t.Fatalf("event types = %s, want %s", got, want)
	}
	if !finished {
		t.Error("turn.completed did not finish the turn")
	}
	if c.threadID == "" {
		t.Error("thread ID not captured")
	}
	if events[1].Tool != "Shell" || !strings.Contains(events[1].Text, "echo ok") {
		t.Errorf("tool event = %+v", events[1])
	}
	if events[3].Text != "done" {
		t.Errorf("reply = %q, want done", events[3].Text)
	}
}
