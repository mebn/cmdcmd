package main

import (
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procMonitorFromPoint = user32.NewProc("MonitorFromPoint")
)

const monitorDefaultToNearest = 2

// cursorScreenID returns the Wails screen ID (the HMONITOR) of the display
// under the cursor.
func cursorScreenID() string {
	var pt struct{ X, Y int32 }
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return ""
	}
	// MonitorFromPoint takes the POINT by value, packed into one argument.
	packed := uintptr(uint32(pt.X)) | uintptr(uint32(pt.Y))<<32
	hMonitor, _, _ := procMonitorFromPoint.Call(packed, monitorDefaultToNearest)
	if hMonitor == 0 {
		return ""
	}
	return strconv.Itoa(int(hMonitor))
}
