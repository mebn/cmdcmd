package hotkey

import (
	"errors"
	"runtime"
	"slices"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrPermission is never returned on Windows; low-level hooks need no grant.
var ErrPermission = errors.New("permission denied")

const (
	whKeyboardLL = 13
	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105
)

// Virtual-key codes (generic, left, right) for each modifier.
var modVKs = map[Modifier][]uint32{
	Ctrl:  {0x11, 0xA2, 0xA3},
	Shift: {0x10, 0xA0, 0xA1},
	Alt:   {0x12, 0xA4, 0xA5},
	Cmd:   {0x5B, 0x5C}, // Windows keys
}

type kbdllHookStruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	procSetWindowsHook  = user32.NewProc("SetWindowsHookExW")
	procCallNextHook    = user32.NewProc("CallNextHookEx")
	procGetMessage      = user32.NewProc("GetMessageW")
	procTranslateMsg    = user32.NewProc("TranslateMessage")
	procDispatchMessage = user32.NewProc("DispatchMessageW")
)

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// SettingsURL is empty on Windows; low-level hooks need no grant.
const SettingsURL = ""

// Start listens for a double tap of mod and calls fire when it happens. ready
// is unused: Windows needs no permission, so Start never has to wait.
func Start(mod Modifier, fire func(), ready func()) error {
	d := newDetector(fire)
	errc := make(chan error, 1)

	go func() {
		// The hook is delivered to the thread that installed it, which must
		// keep pumping messages for as long as the hook should live.
		runtime.LockOSThread()

		callback := windows.NewCallback(func(nCode int, wParam uintptr, lParam uintptr) uintptr {
			if nCode == 0 {
				k := *(**kbdllHookStruct)(unsafe.Pointer(&lParam))
				isTarget := slices.Contains(modVKs[mod], k.VkCode)
				switch {
				case !isTarget && (wParam == wmKeyDown || wParam == wmSysKeyDown):
					d.other()
				case isTarget && (wParam == wmKeyDown || wParam == wmSysKeyDown):
					d.modDown(time.Now())
				case isTarget && (wParam == wmKeyUp || wParam == wmSysKeyUp):
					d.modUp(time.Now())
				}
			}
			r, _, _ := procCallNextHook.Call(0, uintptr(nCode), wParam, lParam)
			return r
		})

		hook, _, err := procSetWindowsHook.Call(whKeyboardLL, callback, 0, 0)
		if hook == 0 {
			errc <- err
			return
		}
		errc <- nil

		var m msg
		for {
			r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			procTranslateMsg.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()

	return <-errc
}
