package hotkey

/*
#cgo CFLAGS: -Wno-unguarded-availability-new
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include "hotkey_darwin.h"
*/
import "C"

import (
	"errors"
	"time"
)

// ErrPermission means the user has not granted Input Monitoring yet. Start
// keeps waiting for the grant and activates the hotkey once it arrives.
var ErrPermission = errors.New("cmdcmd needs Input Monitoring to detect a double-tap hotkey. Enable it in System Settings → Privacy & Security → Input Monitoring")

// SettingsURL opens the system settings pane where the permission is granted.
const SettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent"

var (
	active     *detector
	target     Modifier
	targetDown bool
)

// Virtual keycodes (left, right) and event flag masks for each modifier.
var (
	modKeycodes = map[Modifier][2]C.int{Cmd: {55, 54}, Shift: {56, 60}, Alt: {58, 61}, Ctrl: {59, 62}}
	modFlags    = map[Modifier]uint64{Cmd: 0x100000, Shift: 0x20000, Alt: 0x80000, Ctrl: 0x40000}
)

// allFlags covers every modifier, plus Caps Lock (0x10000) and Fn (0x800000).
const allFlags = 0x100000 | 0x20000 | 0x80000 | 0x40000 | 0x10000 | 0x800000

//export goKeyDown
func goKeyDown() {
	if active != nil {
		active.other()
	}
}

//export goFlagsChanged
func goFlagsChanged(keycode C.int, flags C.ulonglong) {
	if active == nil {
		return
	}
	mask := modFlags[target]
	down := uint64(flags)&mask != 0
	codes := modKeycodes[target]
	isTarget := keycode == codes[0] || keycode == codes[1]
	others := uint64(flags) & (allFlags &^ mask)

	switch {
	case !isTarget || others != 0:
		active.other()
	case down && !targetDown:
		active.modDown(time.Now())
	case !down && targetDown:
		active.modUp(time.Now())
	}
	targetDown = down
}

// Start listens for a double tap of mod and calls fire when it happens. It
// must be called once, after the app's main run loop exists.
//
// If Input Monitoring is not granted, Start shows the system prompt, returns
// ErrPermission, and keeps checking in the background; once the user grants
// it, the hotkey starts working and ready is called. No restart is needed.
func Start(mod Modifier, fire func(), ready func()) error {
	target = mod
	active = newDetector(fire)
	if C.cmdcmdHasAccess() && C.cmdcmdStartTap() {
		return nil
	}
	if C.cmdcmdHasAccess() {
		return errors.New("could not create keyboard event tap")
	}
	C.cmdcmdRequestAccess()
	go func() {
		for range time.Tick(time.Second) {
			if C.cmdcmdHasAccess() && C.cmdcmdStartTap() {
				ready()
				return
			}
		}
	}()
	return ErrPermission
}
