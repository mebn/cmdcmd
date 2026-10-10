# cmdcmd

A Spotlight-style launcher for Claude Code. Double-tap **⌘** (macOS) or **Ctrl** (Windows) to open a chat window backed by a Claude Code session running in the background. Hiding the window (Esc, or clicking away) leaves the session running.

## How it works

- `internal/hotkey`: a system-wide double-tap detector. On macOS it uses a listen-only `CGEventTap`; on Windows, a `WH_KEYBOARD_LL` hook.
- `internal/session`: runs `claude -p --input-format stream-json --output-format stream-json` as a long-lived child process. If the process dies, the next message restarts it with `--resume <session-id>`.
- `main.go`: the window, which is a non-activating floating `NSPanel` on macOS, plus the menu-bar icon (Wails v3).
- `frontend/`: the chat UI (vanilla JS).

## Build & run

Requires the Wails v3 CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28`

```sh
wails3 dev           # live reload
wails3 package       # → bin/cmdcmd.app (macOS)
open bin/cmdcmd.app
```

On the first launch, macOS asks for **Input Monitoring** permission (System Settings → Privacy & Security → Input Monitoring). Grant it, then restart the app. cmdcmd lives only in the menu bar: it has no Dock icon and no ⌘-Tab entry. Click the menu-bar icon to toggle the window. Its menu has New session, Edit config… and Quit.

## Config

The config file is created on first run at `~/Library/Application Support/cmdcmd/config.json` on macOS, or `%AppData%\cmdcmd\config.json` on Windows:

```json
{
  "cwd": "~",
  "permissionMode": "auto",
  "model": "",
  "claudePath": ""
}
```

- `cwd` is where Claude runs, so project-level skills and CLAUDE.md are loaded from there. User-level skills (`~/.claude/skills`) are always available.
- `permissionMode` is passed to `--permission-mode`.

## Shortcuts

`Enter` send · `Shift+Enter` newline · `Esc` hide · `⌘N` new session · `⌘Q` quit
