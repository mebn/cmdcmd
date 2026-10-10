package session

import (
	"bufio"
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
	s := New(Options{}, func(ev Event) { events = append(events, ev) })
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
