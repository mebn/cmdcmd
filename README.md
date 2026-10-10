# cmdcmd

Double-tap **⌘** (macOS) or **Ctrl** (Windows) to chat with Claude Code, Codex or a custom command running in the background.

<p>
  <img width="49%" alt="closed" src="https://github.com/user-attachments/assets/888a406b-8e88-4b0a-a8ef-80bcbfef60ce" />
  <img width="49%" alt="open" src="https://github.com/user-attachments/assets/7ef372c1-8425-47a8-8bed-12c4427ebda9" />
</p>

## Build

Needs Go and the Wails v3 CLI (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28`).

```sh
wails3 dev       # live reload
wails3 package   # → bin/cmdcmd.app
```

On first launch, grant **Input Monitoring** when macOS asks. cmdcmd lives in the menu bar.

## Config

`~/Library/Application Support/cmdcmd/config.jsonc` (macOS) or `%AppData%\cmdcmd\config.jsonc` (Windows). Every option is documented in the file. Open it from the menu bar with **Edit config…**.
