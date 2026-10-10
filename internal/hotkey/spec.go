package hotkey

import (
	"fmt"
	"runtime"
	"strings"
)

// Default is the default hotkey: double-tap ⌘ on macOS, Ctrl on Windows.
const Default = "mod mod"

// Modifier is a modifier key.
type Modifier int

const (
	Cmd  Modifier = iota // ⌘ on macOS, the Windows key on Windows
	Ctrl                 // Control
	Alt                  // Option on macOS
	Shift
)

// Spec is a parsed hotkey: either a double tap of one modifier, or a key
// combination registered as a global shortcut.
type Spec struct {
	DoubleTap   bool
	Modifier    Modifier // the tapped modifier, for a double tap
	Accelerator string   // Wails accelerator syntax, for a combination
	Label       string   // how the hotkey is shown to the user
}

var modifierNames = map[string]Modifier{
	"cmd": Cmd, "command": Cmd, "⌘": Cmd, "win": Cmd, "super": Cmd, "meta": Cmd,
	"ctrl": Ctrl, "control": Ctrl, "⌃": Ctrl,
	"alt": Alt, "option": Alt, "opt": Alt, "⌥": Alt,
	"shift": Shift, "⇧": Shift,
}

// Parse reads a hotkey such as "mod mod" (double-tap) or "mod+shift+space"
// (combination). "mod" is ⌘ on macOS and Ctrl elsewhere.
func Parse(s string) (Spec, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		s = Default
	}

	// Double tap: the same modifier twice, separated by a space.
	if fields := strings.Fields(s); len(fields) == 2 && !strings.Contains(s, "+") {
		a, okA := modifier(fields[0])
		b, okB := modifier(fields[1])
		if !okA || !okB || a != b {
			return Spec{}, fmt.Errorf("hotkey %q: a double tap is one modifier twice, like \"mod mod\"", s)
		}
		label := modifierLabel(a)
		return Spec{DoubleTap: true, Modifier: a, Label: label + " " + label}, nil
	}

	// Combination: modifiers and one key joined by "+".
	parts := strings.Split(strings.ReplaceAll(s, " ", ""), "+")
	key := parts[len(parts)-1]
	if key == "" || len(parts) < 2 {
		return Spec{}, fmt.Errorf("hotkey %q: use a double tap like \"mod mod\" or a combination like \"mod+space\"", s)
	}
	if _, isMod := modifier(key); isMod {
		return Spec{}, fmt.Errorf("hotkey %q: a combination must end with a key, like \"mod+space\"", s)
	}
	var accel, labels []string
	for _, p := range parts[:len(parts)-1] {
		m, ok := modifier(p)
		if !ok {
			return Spec{}, fmt.Errorf("hotkey %q: unknown modifier %q", s, p)
		}
		accel = append(accel, acceleratorName(m))
		labels = append(labels, modifierLabel(m))
	}
	accel = append(accel, key)
	labels = append(labels, keyLabel(key))

	sep := "+"
	if runtime.GOOS == "darwin" {
		sep = "" // ⌘⇧Space, as macOS menus write it
	}
	return Spec{Accelerator: strings.Join(accel, "+"), Label: strings.Join(labels, sep)}, nil
}

func modifier(name string) (Modifier, bool) {
	if name == "mod" {
		if runtime.GOOS == "darwin" {
			return Cmd, true
		}
		return Ctrl, true
	}
	m, ok := modifierNames[name]
	return m, ok
}

// acceleratorName is the modifier in Wails accelerator syntax, where "cmd"
// means Cmd-or-Ctrl and "super" is the Windows key.
func acceleratorName(m Modifier) string {
	switch m {
	case Cmd:
		if runtime.GOOS == "darwin" {
			return "cmd"
		}
		return "super"
	case Ctrl:
		return "ctrl"
	case Alt:
		return "alt"
	default:
		return "shift"
	}
}

func modifierLabel(m Modifier) string {
	if runtime.GOOS == "darwin" {
		return [...]string{"⌘", "⌃", "⌥", "⇧"}[m]
	}
	return [...]string{"Win", "Ctrl", "Alt", "Shift"}[m]
}

func keyLabel(key string) string {
	if len(key) == 1 {
		return strings.ToUpper(key)
	}
	return strings.ToUpper(key[:1]) + key[1:]
}
