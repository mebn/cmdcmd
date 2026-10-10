# cmdcmd

A Spotlight-style launcher for Claude Code. Double-tap **⌘** (macOS) or **Ctrl** (Windows) to open a chat window backed by a Claude Code session running in the background. Hiding the window (Esc, or clicking away) leaves the session running.

<img width="1800" height="1169" alt="closed" src="https://github.com/user-attachments/assets/888a406b-8e88-4b0a-a8ef-80bcbfef60ce" />

<img width="1800" height="1169" alt="open" src="https://github.com/user-attachments/assets/7ef372c1-8425-47a8-8bed-12c4427ebda9" />


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

Most settings live in the app itself: the agent, model and effort pickers, and the custom agent's command. Everything else is in a config file, which you can open from the menu-bar icon (**Edit config…**). It's created on first run at `~/Library/Application Support/cmdcmd/config.jsonc` on macOS, or `%AppData%\cmdcmd\config.jsonc` on Windows.

It's JSON with comments, and every setting is documented inline with its options. cmdcmd rewrites the file when you change a setting in the app, so comments you add yourself aren't kept. Restart cmdcmd after editing it.

## Shortcuts

`Enter` send · `Shift+Enter` newline · `Esc` hide · `⌘N` new session · `⌘Q` quit
