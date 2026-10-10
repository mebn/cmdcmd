// Package config loads cmdcmd's settings file and resolves the environment
// claude runs in.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Config is stored as JSON in the user config dir (see Path).
type Config struct {
	// Cwd is the directory claude runs in; project skills and CLAUDE.md are
	// picked up from here. "~" is expanded.
	Cwd string `json:"cwd"`
	// PermissionMode is passed as --permission-mode.
	PermissionMode string `json:"permissionMode"`
	// Model is passed as --model when set.
	Model string `json:"model,omitempty"`
	// ClaudePath overrides where the claude binary is found.
	ClaudePath string `json:"claudePath,omitempty"`
}

func defaults() Config {
	return Config{Cwd: "~", PermissionMode: "auto"}
}

// Path is where the config file lives, e.g.
// ~/Library/Application Support/cmdcmd/config.json on macOS.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cmdcmd", "config.json"), nil
}

// Load reads the config, writing the defaults on first run.
func Load() (Config, error) {
	cfg := defaults()
	path, err := Path()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return cfg, err
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		return cfg, os.WriteFile(path, append(data, '\n'), 0o644)
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaults(), err
	}
	return cfg, nil
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

// FindClaude finds the claude binary: the configured path, then PATH from
// env, then the usual install locations.
func (c Config) FindClaude(env []string) string {
	if c.ClaudePath != "" {
		return c.ClaudePath
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
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
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
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
