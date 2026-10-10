package session

import "testing"

func TestUserText(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"log 2h on project X", "log 2h on project X"},
		{"<command-message>timelog</command-message>\n<command-name>/timelog</command-name>\n<command-args>2h project X</command-args>", "/timelog 2h project X"},
		{"<command-name>/clear</command-name>", "/clear"},
		{"<local-command-stdout>ok</local-command-stdout>", ""},
		{"  ", ""},
	} {
		if got := userText(tt.in); got != tt.want {
			t.Errorf("userText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestClaudeItems(t *testing.T) {
	user := `{"type":"user","message":{"role":"user","content":"hello"}}`
	meta := `{"type":"user","isMeta":true,"message":{"role":"user","content":"hidden"}}`
	result := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	assistant := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"},{"type":"tool_use","name":"Bash","input":{"command":"echo ok"}}]}}`

	var got []HistoryItem
	for _, l := range []string{user, meta, result, assistant} {
		got = appendMerged(got, claudeItems([]byte(l))...)
	}
	want := []HistoryItem{
		{Role: "user", Text: "hello"},
		{Role: "assistant", Text: "hi"},
		{Role: "tool", Tool: "Bash", Text: "echo ok"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
