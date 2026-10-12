package config

import (
	"bytes"
	"encoding/json"
	"text/template"
)

// fileTemplate is how config.jsonc is written: every setting with a comment
// listing its options. Save renders it with the current values, so the
// documentation is always there, but comments added by hand are not kept.
var fileTemplate = template.Must(template.New("config").Funcs(template.FuncMap{
	"json": func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	},
}).Parse(`// cmdcmd settings.
//
// cmdcmd rewrites this file when you change a setting in the app (agent,
// model, effort, custom command), so comments you add here are not kept.
// Restart cmdcmd (menu bar icon → Quit, then open it again) after editing.
{
  // Directory the agent runs in. Project skills, CLAUDE.md and AGENTS.md are
  // picked up from here. "~" is your home folder.
  "cwd": {{json .Cwd}},

  // Shortcut that shows and hides cmdcmd. "mod" is ⌘ on macOS, Ctrl on Windows.
  //   Double-tap a modifier:  "mod mod" (default), "shift shift", "alt alt",
  //                           "ctrl ctrl", "cmd cmd" (Windows key on Windows)
  //   Key combination:        "mod+space", "ctrl+alt+k", "mod+shift+g"
  // Modifiers: mod, cmd, ctrl, alt (option), shift. Keys: a-z, 0-9, space,
  // return, tab, escape, f1-f12. Double taps need Input Monitoring on macOS;
  // combinations don't.
  "hotkey": {{json .Hotkey}},

  // Which agent answers your messages. Also set from the Agent picker.
  //   "claude"  Claude Code (claude CLI)
  //   "codex"   OpenAI Codex (codex CLI)
  //   "custom"  the command in "custom" below
  "agent": {{json .Agent}},

  // Model for the selected agent. Also set from the Model picker.
  //   ""        the agent's own default
  //   Claude:   "default", "opus", "sonnet", "haiku", "fable",
  //             or a full model ID such as "claude-opus-5-5"
  //   Codex:    a model slug from ~/.codex/models_cache.json, e.g. "gpt-6-astra"
  "model": {{json .Model}},

  // Reasoning effort. Also set from the Effort picker; which levels exist
  // depends on the model.
  //   ""        the model's default
  //   "low", "medium", "high", "xhigh", "max"
  //   Codex also: "minimal" or "ultra" on some models
  "effort": {{json .Effort}},

  // How Claude decides whether a tool may run (claude --permission-mode).
  // cmdcmd cannot show permission prompts, so anything that would prompt is
  // denied instead.
  //   "auto"               Claude decides, blocking risky actions (default)
  //   "manual"             every tool needs pre-approval in Claude settings.json
  //   "acceptEdits"        allow file edits; other tools need pre-approval
  //   "plan"               plan only, change nothing
  //   "dontAsk"            deny anything not pre-approved in settings.json
  //   "bypassPermissions"  allow everything, no checks
  "permissionMode": {{json .PermissionMode}},

  // Where the claude binary is. "" finds it on your PATH or in ~/.local/bin.
  "claudePath": {{json .ClaudePath}},

  // What shell commands Codex may run (codex sandbox_mode).
  //   "read-only"           read files only
  //   "workspace-write"     write inside cwd, no network (default)
  //   "danger-full-access"  no sandbox: full disk and network
  "codexSandbox": {{json .CodexSandbox}},

  // Where the codex binary is. "" finds it on your PATH.
  "codexPath": {{json .CodexPath}},

  // The custom agent: a shell command that gets each message on stdin and
  // replies on stdout. The command is also set from the Agent picker.
  "custom": {
    // Name shown in the Agent picker.
    "name": {{json .Custom.Name}},
    // Shell command line, e.g. "ollama run llama3". "" disables the agent.
    "command": {{json .Custom.Command}}
  },

  // Windows only: where agents run. Also set in Settings.
  //   "windows"  Claude Code installed on Windows (default)
  //   "wsl"      Claude Code installed in WSL
  //   "both"     folders and sessions from both; each runs where it belongs
  "environment": {{json .Environment}},

  // Windows only: the WSL distro to use. "" is the default distro
  // (wsl --set-default).
  "wslDistro": {{json .WSLDistro}}
}
`))

func render(c Config) []byte {
	var buf bytes.Buffer
	if err := fileTemplate.Execute(&buf, c); err != nil {
		panic(err) // the template and Config are fixed; this is a programming error
	}
	return buf.Bytes()
}
