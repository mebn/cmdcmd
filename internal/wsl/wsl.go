// Package wsl runs agents inside the Windows Subsystem for Linux and maps
// between its paths and Windows ones.
//
// A directory in WSL is kept as its Linux path, e.g. /home/me/code. On
// Windows no native path starts with "/", so the path alone tells which side
// a folder or session is on.
package wsl

import (
	"context"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// IsPath reports whether p is a directory in WSL rather than on Windows.
func IsPath(p string) bool {
	return runtime.GOOS == "windows" && strings.HasPrefix(p, "/")
}

// Clean cleans a path the way its side expects; filepath.Clean would turn a
// Linux path's slashes into backslashes on Windows.
func Clean(p string) string {
	if IsPath(p) {
		return path.Clean(p)
	}
	return filepath.Clean(p)
}

var (
	mu         sync.Mutex
	distro     string // "" until Use or the first lookup
	configured bool
	home       string // the distro user's home, as a Linux path
	homeDone   bool
	found      = map[string]bool{}
)

// Use sets the distro to run in; "" means the default one.
func Use(name string) {
	mu.Lock()
	defer mu.Unlock()
	distro, configured = name, name != ""
	home, homeDone = "", false
	clear(found)
}

// Distro returns the distro in use, "" if WSL has none.
func Distro() string {
	mu.Lock()
	defer mu.Unlock()
	if distro == "" && !configured {
		distro = defaultDistro()
	}
	return distro
}

// Available reports whether WSL is installed with a distro.
func Available() bool {
	return Distro() != ""
}

// Home returns the distro user's home folder as a Linux path, "" if WSL
// isn't available. The first call starts the distro if it isn't running.
func Home() string {
	d := Distro()
	mu.Lock()
	defer mu.Unlock()
	if homeDone || d == "" {
		return home
	}
	homeDone = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-d", d, "--exec", "sh", "-c", `printf %s "$HOME"`)
	hideWindow(cmd)
	if out, err := cmd.Output(); err == nil && strings.HasPrefix(string(out), "/") {
		home = path.Clean(string(out))
	}
	return home
}

// Has reports whether a program is on the PATH of the user's login shell in
// WSL. The answer is kept for the rest of the run.
func Has(name string) bool {
	d := Distro()
	if d == "" {
		return false
	}
	mu.Lock()
	ok, done := found[name]
	mu.Unlock()
	if done {
		return ok
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	program, args := Command(d, "", "sh", "-c", `command -v "$1" >/dev/null`, "sh", name)
	cmd := exec.CommandContext(ctx, program, args...)
	hideWindow(cmd)
	ok = cmd.Run() == nil
	mu.Lock()
	found[name] = ok
	mu.Unlock()
	return ok
}

// loginShell runs its arguments ("$@": the program, then its arguments)
// through the user's login shell, interactive so that PATH set in .bashrc or
// .zshrc (nvm, ~/.local/bin) applies too. Arguments are passed, never
// parsed, so they need no quoting.
const loginShell = `exec "${SHELL:-/bin/sh}" -lic 'exec "$0" "$@"' "$@"`

// Command returns the Windows program and arguments that run name with args
// in the distro, in the Linux directory cwd ("" for the home folder).
func Command(distro, cwd, name string, args ...string) (string, []string) {
	out := []string{"-d", distro}
	if cwd == "" {
		cwd = "~"
	}
	// sh -c takes the argument after the script as $0, so "sh" goes first.
	out = append(out, "--cd", cwd, "--exec", "sh", "-c", loginShell, "sh", name)
	return "wsl.exe", append(out, args...)
}

// Native returns a path Windows can open: a WSL path becomes the
// \\wsl$\<distro>\… share; any other path is returned as is.
func Native(p string) string {
	if !IsPath(p) {
		return p
	}
	d := Distro()
	if d == "" {
		return p
	}
	return `\\wsl$\` + d + strings.ReplaceAll(p, "/", `\`)
}

// IsShare reports whether p is in a \\wsl$ or \\wsl.localhost share.
func IsShare(p string) bool {
	lower := strings.ToLower(p)
	return strings.HasPrefix(lower, `\\wsl$\`) || strings.HasPrefix(lower, `\\wsl.localhost\`)
}

// FromWindows returns the WSL path for a Windows path: a \\wsl$ or
// \\wsl.localhost share of the distro in use maps back to its Linux path,
// and a drive path such as C:\code to /mnt/c/code. It returns "" for a path
// WSL can't reach this way.
func FromWindows(p string) string {
	if IsPath(p) {
		return path.Clean(p)
	}
	p = filepath.Clean(p)
	lower := strings.ToLower(p)
	for _, share := range []string{`\\wsl$\`, `\\wsl.localhost\`} {
		if !strings.HasPrefix(lower, share) {
			continue
		}
		name, rest, _ := strings.Cut(p[len(share):], `\`)
		if !strings.EqualFold(name, Distro()) {
			return ""
		}
		return path.Clean("/" + strings.ReplaceAll(rest, `\`, "/"))
	}
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(p[:1])
		return path.Clean("/mnt/" + drive + "/" + strings.ReplaceAll(p[2:], `\`, "/"))
	}
	return ""
}
