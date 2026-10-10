package config

import (
	"encoding/json"
	"testing"
)

func TestCustomAgentCommandForms(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`{"name":"X","command":"ollama run llama3"}`, "ollama run llama3"},
		{`{"name":"X","command":["ollama","run","llama3"]}`, "ollama run llama3"},
		{`{"name":"X","command":[]}`, ""},
	} {
		var c CustomAgent
		if err := json.Unmarshal([]byte(tt.in), &c); err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if c.Command != tt.want || c.Name != "X" {
			t.Errorf("%s: got %+v, want command %q", tt.in, c, tt.want)
		}
	}
}

func TestStripJSONC(t *testing.T) {
	src := `{
  // line comment
  "a": "keep // this and /* this */", /* block */
  "b": [1, 2,],
  "c": {"d": "\"quoted\" // not a comment",},
}`
	var got map[string]any
	if err := json.Unmarshal(stripJSONC([]byte(src)), &got); err != nil {
		t.Fatalf("stripped output is not JSON: %v\n%s", err, stripJSONC([]byte(src)))
	}
	if got["a"] != "keep // this and /* this */" {
		t.Errorf("a = %q", got["a"])
	}
	if d := got["c"].(map[string]any)["d"]; d != `"quoted" // not a comment` {
		t.Errorf("d = %q", d)
	}
}

// The written file must read back to the same config.
func TestRenderRoundTrip(t *testing.T) {
	want := defaults()
	want.Model = "opus"
	want.Effort = "high"
	want.Custom.Command = `ollama run "llama3"`
	got := Config{}
	if err := json.Unmarshal(stripJSONC(render(want)), &got); err != nil {
		t.Fatalf("rendered config does not parse: %v\n%s", err, render(want))
	}
	if got != want {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
	}
}
