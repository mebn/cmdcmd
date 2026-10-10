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
var ErrPermission = errors.New("cmdcmd needs Input Monitoring to detect ⌘ ⌘. Enable it in System Settings → Privacy & Security → Input Monitoring")

// SettingsURL opens the system settings pane where the permission is granted.
const SettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent"

// Description is the shortcut as shown to the user.
const Description = "⌘ ⌘"

var active *detector

//export goKeyEvent
func goKeyEvent(kind C.int) {
	if active == nil {
		return
	}
	switch kind {
	case C.kindCmdDown:
		active.modDown(time.Now())
	case C.kindCmdUp:
		active.modUp(time.Now())
	default:
		active.other()
	}
}

// Start listens for a Cmd double tap and calls fire when it happens. It must
// be called once, after the app's main run loop exists.
//
// If Input Monitoring is not granted, Start shows the system prompt, returns
// ErrPermission, and keeps checking in the background; once the user grants
// it, the hotkey starts working and ready is called. No restart is needed.
func Start(fire func(), ready func()) error {
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
