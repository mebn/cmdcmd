// Package config loads cmdcmd's settings file and resolves the environment
// claude runs in.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Config is stored as JSONC (JSON with comments) in the user config dir; see
// Path and template.go.
type Config struct {
	// Cwd is the directory agents run in; project skills and CLAUDE.md are
	// picked up from here. "~" is expanded.
	Cwd string `json:"cwd"`

	// Agent is the selected agent: "claude", "codex" or "custom". Model and
	// Effort are its selected model and effort level ("" means its default).
	Agent  string `json:"agent"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`

	// PermissionMode is passed to claude as --permission-mode.
	PermissionMode string `json:"permissionMode"`
	// ClaudePath overrides where the claude binary is found.
	ClaudePath string `json:"claudePath,omitempty"`

	// CodexSandbox is Codex's sandbox_mode: "read-only", "workspace-write"
	// or "danger-full-access".
	CodexSandbox string `json:"codexSandbox"`
	// CodexPath overrides where the codex binary is found.
	CodexPath string `json:"codexPath,omitempty"`

	// Custom is a command that gets each message on stdin and replies on
	// stdout, set from the agent picker.
	Custom CustomAgent `json:"custom"`
}

// CustomAgent is a user-defined agent: a shell command line such as
// "ollama run llama3".
type CustomAgent struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// UnmarshalJSON also accepts the older form where command was a list of
// arguments.
func (c *CustomAgent) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name    string          `json:"name"`
		Command json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Name = raw.Name
	var args []string
	if json.Unmarshal(raw.Command, &args) == nil {
		c.Command = strings.Join(args, " ")
		return nil
	}
	return json.Unmarshal(raw.Command, &c.Command)
}

// Shell returns the program and arguments that run a command line through
// the system shell.
func Shell(commandLine string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", commandLine}
	}
	return "/bin/sh", []string{"-c", commandLine}
}

func defaults() Config {
	return Config{
		Cwd:            "~",
		Agent:          "claude",
		PermissionMode: "auto",
		CodexSandbox:   "workspace-write",
		Custom:         CustomAgent{Name: "Custom"},
	}
}

// Path is where the config file lives, e.g.
// ~/Library/Application Support/cmdcmd/config.jsonc on macOS.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cmdcmd", "config.jsonc"), nil
}

// Load reads the config. On first run it writes the documented defaults, or
// converts a config.json from older versions.
func Load() (Config, error) {
	cfg := defaults()
	path, err := Path()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return migrate(path)
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(stripJSONC(data), &cfg); err != nil {
		return defaults(), fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// migrate converts config.json next to path into config.jsonc, or writes the
// defaults if there is none.
func migrate(path string) (Config, error) {
	cfg := defaults()
	legacy := strings.TrimSuffix(path, "c")
	data, err := os.ReadFile(legacy)
	if err != nil {
		return cfg, cfg.Save()
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaults(), fmt.Errorf("%s: %w", legacy, err)
	}
	if err := cfg.Save(); err != nil {
		return cfg, err
	}
	return cfg, os.Remove(legacy)
}

// Save writes the config file, with every option documented.
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, render(c), 0o644)
}

// ResolvedCwd returns Cwd with "~" expanded.
func (c Config) ResolvedCwd() string {
	home, _ := os.UserHomeDir()
	switch {
	case c.Cwd == "" || c.Cwd == "~":
		return home
	case strings.HasPrefix(c.Cwd, "~/"):
		return filepath.Join(home, c.Cwd[2:])
	}
	return c.Cwd
}

// Env returns the environment claude should run with. Apps launched from
// Finder get a minimal PATH, so on macOS the login shell's PATH is used; the
// tools your skills call are usually only on that PATH.
func Env() []string {
	env := os.Environ()
	if runtime.GOOS != "darwin" {
		return env
	}
	path := loginShellPath()
	if path == "" {
		return env
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+path)
}

func loginShellPath() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, shell, "-ilc", `printf '__CMDCMD__%s' "$PATH"`).Output()
	if _, path, ok := strings.Cut(string(out), "__CMDCMD__"); ok {
		return strings.TrimSpace(path)
	}
	return ""
}

// FindClaude finds the claude binary.
func (c Config) FindClaude(env []string) string {
	return findBinary("claude", c.ClaudePath, env)
}

// FindCodex finds the codex binary.
func (c Config) FindCodex(env []string) string {
	return findBinary("codex", c.CodexPath, env)
}

// findBinary returns the configured path, else the first match on PATH from
// env, else one of the usual install locations, else just the name.
func findBinary(name, configured string, env []string) string {
	if configured != "" {
		return configured
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, kv := range env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			for _, dir := range filepath.SplitList(p) {
				candidate := filepath.Join(dir, name)
				if isExecutable(candidate) {
					return candidate
				}
			}
		}
	}
	home, _ := os.UserHomeDir()
	for _, candidate := range []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, ".claude", "local", name),
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
	} {
		if isExecutable(candidate) {
			return candidate
		}
	}
	return name
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Found reports whether path, as returned by FindClaude or FindCodex, points
// at an installed binary rather than a bare name that was not found.
func Found(path string) bool {
	return filepath.IsAbs(path) && isExecutable(path)
}
