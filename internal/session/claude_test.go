package session

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"
)

// turn.jsonl is a real `claude -p --output-format stream-json` transcript of
// one turn: a short reply, a Bash call, and a closing reply.
func TestHandleTurn(t *testing.T) {
	f, err := os.Open("testdata/turn.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var events []Event
	s := NewClaude(Options{}, func(ev Event) { events = append(events, ev) })
	s.running = true // as Send leaves it
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		s.handle(sc.Bytes())
	}

	var types []string
	var text strings.Builder
	for _, ev := range events {
		if ev.Type == "text" {
			text.WriteString(ev.Text)
			continue
		}
		types = append(types, ev.Type)
	}

	want := []string{"init", "block", "tool", "block", "done"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("event types = %v, want %v", types, want)
	}
	if s.ID() == "" {
		t.Error("session ID not captured from init")
	}
	if got := text.String(); !strings.Contains(got, "Hello there, friend!") || !strings.Contains(got, "printed `ok`") {
		t.Errorf("streamed text = %q", got)
	}
	for _, ev := range events {
		if ev.Type == "tool" && (ev.Tool != "Bash" || ev.Text != "Print ok") {
			t.Errorf("tool event = %+v, want Bash / Print ok", ev)
		}
		if ev.Type == "done" && (ev.IsError || ev.Cost == 0) {
			t.Errorf("done event = %+v", ev)
		}
	}
}

// background.jsonl is a real, trimmed stream of a turn that starts a
// background shell and ends while it runs, then the turn Claude takes on its
// own when the shell finishes.
func TestHandleBackgroundTask(t *testing.T) {
	data, err := os.ReadFile("testdata/background.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	s := NewClaude(Options{}, func(ev Event) { events = append(events, ev) })
	s.running = true // as Send leaves it
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		s.handle([]byte(line))
	}

	var got []string
	for _, ev := range events {
		switch ev.Type {
		case "tasks":
			got = append(got, fmt.Sprintf("tasks:%d", len(ev.Tasks)))
		case "done":
			got = append(got, fmt.Sprintf("done:%v", ev.Idle))
		case "init", "start":
			got = append(got, ev.Type)
		}
	}
	want := "init tasks:1 done:true tasks:0 init start done:true"
	if strings.Join(got, " ") != want {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for _, ev := range events {
		if ev.Type == "tasks" && len(ev.Tasks) == 1 && (ev.Tasks[0].Kind != "local_bash" || ev.Tasks[0].Description == "") {
			t.Errorf("task = %+v", ev.Tasks[0])
		}
	}
	if s.running {
		t.Error("still running after the last result")
	}
}

// initialize.jsonl is a trimmed reply to the initialize control request.
func TestHandleInitialize(t *testing.T) {
	data, err := os.ReadFile("testdata/initialize.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var got []Event
	s := NewClaude(Options{}, func(ev Event) { got = append(got, ev) })
	s.handle(data)

	if len(got) != 1 || got[0].Type != "commands" {
		t.Fatalf("events = %+v, want one commands event", got)
	}
	var names []string
	for _, c := range got[0].Commands {
		names = append(names, c.Name)
		if c.Description == "" {
			t.Errorf("command %q has no description", c.Name)
		}
	}
	if strings.Join(names, ",") != "code-review,loop,compact" {
		t.Errorf("names = %v, want internal __ commands dropped", names)
	}
}
