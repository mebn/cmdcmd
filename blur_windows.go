package main

import (
	"math"
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"
)

// On Windows the blur is drawn by Windows.UI.Composition in a second window,
// the backdrop, kept right under the transparent main window at the same
// place and size. Each card gets a visual filled with the host backdrop
// brush (the blurred desktop behind the window) and clipped to the card's
// rounded rectangle. Window regions can't do this: Windows 11 draws the
// acrylic effect over a window's whole bounds whatever region it is cut to.
//
// Composition objects are COM interfaces, called through their vtables; the
// slots and IIDs are from the Windows SDK's windows.ui.composition.h.

var (
	procRegisterClassExW  = user32.NewProc("RegisterClassExW")
	procCreateWindowExW   = user32.NewProc("CreateWindowExW")
	procDefWindowProcW    = user32.NewProc("DefWindowProcW")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procShowWindow        = user32.NewProc("ShowWindow")
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procGetDpiForWindow   = user32.NewProc("GetDpiForWindow")
	procIsWindowVisible   = user32.NewProc("IsWindowVisible")
	procDwmSetWindowAttr  = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	procCreateDispatcherQ = windows.NewLazySystemDLL("CoreMessaging.dll").NewProc("CreateDispatcherQueueController")
	combase               = windows.NewLazySystemDLL("combase.dll")
	procRoActivate        = combase.NewProc("RoActivateInstance")
	procWindowsCreateStr  = combase.NewProc("WindowsCreateString")
	procWindowsDeleteStr  = combase.NewProc("WindowsDeleteString")
)

const (
	wsPopup                  = 0x80000000
	wsExTopmost              = 0x00000008
	wsExTransparent          = 0x00000020
	wsExToolWindow           = 0x00000080
	wsExNoRedirectionBitmap  = 0x00200000
	wsExNoActivate           = 0x08000000
	swpNoActivate            = 0x0010
	swpShowWindow            = 0x0040
	swpNoOwnerZOrder         = 0x0200
	swHide                   = 0
	dwmaUseHostBackdropBrush = 17
	dqThreadCurrent          = 2
)

var (
	iidCompositor        = windows.GUID{Data1: 0xb403ca50, Data2: 0x7f8c, Data3: 0x4e83, Data4: [8]byte{0x98, 0x5f, 0xcc, 0x45, 0x06, 0x00, 0x36, 0xd8}}
	iidCompositor3       = windows.GUID{Data1: 0xc9dd8ef0, Data2: 0x6eb1, Data3: 0x4e3c, Data4: [8]byte{0xa6, 0x58, 0x67, 0x5d, 0x9c, 0x64, 0xd4, 0xab}}
	iidCompositor7       = windows.GUID{Data1: 0xd3483fad, Data2: 0x9a12, Data3: 0x53ba, Data4: [8]byte{0xbf, 0xc8, 0x88, 0xb7, 0xff, 0x79, 0x77, 0xc6}}
	iidDesktopInterop    = windows.GUID{Data1: 0x29e691fa, Data2: 0x4567, Data3: 0x4dca, Data4: [8]byte{0xb3, 0x19, 0xd0, 0xf2, 0x07, 0xeb, 0x68, 0x07}}
	iidCompositionTarget = windows.GUID{Data1: 0xa1bea8ba, Data2: 0xd726, Data3: 0x4663, Data4: [8]byte{0x81, 0x29, 0x6b, 0x5e, 0x79, 0x27, 0xff, 0xa6}}
	iidContainerVisual   = windows.GUID{Data1: 0x02f6bc74, Data2: 0xed20, Data3: 0x4773, Data4: [8]byte{0xaf, 0xe6, 0xd4, 0x9b, 0x4a, 0x93, 0xdb, 0x32}}
	iidVisual            = windows.GUID{Data1: 0x117e202d, Data2: 0xa859, Data3: 0x4c89, Data4: [8]byte{0x87, 0x3b, 0xc2, 0xaa, 0x56, 0x67, 0x88, 0xe3}}
	iidCompositionBrush  = windows.GUID{Data1: 0xab0d7608, Data2: 0x30c0, Data3: 0x40e9, Data4: [8]byte{0xb5, 0x68, 0xb6, 0x0a, 0x6b, 0xd1, 0xfb, 0x46}}
	iidCompositionClip   = windows.GUID{Data1: 0x1ccd2a52, Data2: 0xcfc7, Data3: 0x4ace, Data4: [8]byte{0x99, 0x83, 0x14, 0x6b, 0xb8, 0xeb, 0x6a, 0x3c}}
)

// Vtable slots. Slots 0-2 are IUnknown's, 3-5 IInspectable's.
const (
	slotQueryInterface          = 0
	slotCreateContainerVisual   = 9  // ICompositor
	slotCreateSpriteVisual      = 22 // ICompositor
	slotCreateHostBackdropBrush = 6  // ICompositor3
	slotCreateRectangleClip     = 8  // ICompositor7
	slotCreateDesktopTarget     = 3  // ICompositorDesktopInterop
	slotPutRoot                 = 7  // ICompositionTarget
	slotGetChildren             = 6  // IContainerVisual
	slotInsertAtTop             = 9  // IVisualCollection
	slotPutBrush                = 7  // ISpriteVisual
	slotPutClip                 = 15 // IVisual
	slotPutIsVisible            = 19
	slotPutOffset               = 21
	slotPutSize                 = 36
	slotPutBottom               = 7 // IRectangleClip
	slotPutBottomLeftRadius     = 9
	slotPutBottomRightRadius    = 11
	slotPutLeft                 = 13
	slotPutRight                = 15
	slotPutTop                  = 17
	slotPutTopLeftRadius        = 19
	slotPutTopRightRadius       = 21
)

// comObject is a COM interface pointer.
type comObject struct{ vtbl *[64]uintptr }

// call calls the method in a vtable slot; float arguments go as their bits.
func (o *comObject) call(slot int, args ...uintptr) int32 {
	r, _, _ := syscall.SyscallN(o.vtbl[slot], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return int32(r)
}

// get calls a method that returns an interface through its last argument.
func (o *comObject) get(slot int, args ...uintptr) *comObject {
	var out *comObject
	if o.call(slot, append(args, uintptr(unsafe.Pointer(&out)))...) < 0 {
		return nil
	}
	return out
}

func (o *comObject) query(iid *windows.GUID) *comObject {
	return o.get(slotQueryInterface, uintptr(unsafe.Pointer(iid)))
}

func f32(v float64) uintptr { return uintptr(math.Float32bits(float32(v))) }

// vector2 packs a Vector2, which is passed by value in one register.
func vector2(x, y float64) uintptr { return f32(x) | f32(y)<<32 }

// cardVisual is one card's blur: a sprite visual and its rounded clip.
type cardVisual struct {
	visual *comObject // IVisual
	clip   *comObject // IRectangleClip
}

// backdrop is the blur window and its composition tree. It is only touched
// on the main thread, which runs the dispatcher queue composition needs.
var backdrop struct {
	once       sync.Once
	main, hwnd uintptr
	compositor *comObject // ICompositor
	clips      *comObject // ICompositor7
	brush      *comObject // ICompositionBrush
	children   *comObject // IVisualCollection
	cards      []cardVisual
	shapes     []BlurShape
}

func setBlur(window *application.WebviewWindow, shapes []BlurShape) {
	backdrop.once.Do(func() {
		main := uintptr(window.NativeWindow())
		application.InvokeSync(func() { createBackdrop(main) })
		// Follow the main window around, and in and out of view.
		window.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) {
			application.InvokeAsync(placeBackdrop)
		})
		window.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) {
			application.InvokeAsync(placeBackdrop)
		})
		window.OnWindowEvent(events.Common.WindowHide, func(*application.WindowEvent) {
			application.InvokeAsync(func() { procShowWindow.Call(backdrop.hwnd, swHide) })
		})
	})
	application.InvokeAsync(func() {
		backdrop.shapes = shapes
		shapeCards()
		placeBackdrop()
	})
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// createBackdrop makes the blur window and its composition tree. The window
// never activates or takes clicks: focus moving to it would hide the main
// window. If composition isn't available, there is simply no blur.
func createBackdrop(main uintptr) {
	backdrop.main = main
	class, _ := windows.UTF16PtrFromString("cmdcmdBackdrop")
	wc := wndClassEx{WndProc: procDefWindowProcW.Addr(), ClassName: class}
	windows.GetModuleHandleEx(0, nil, &wc.Instance)
	wc.Size = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := procCreateWindowExW.Call(
		wsExTopmost|wsExTransparent|wsExToolWindow|wsExNoActivate|wsExNoRedirectionBitmap,
		uintptr(unsafe.Pointer(class)), 0, wsPopup,
		0, 0, 0, 0, 0, 0, uintptr(wc.Instance), 0)
	if hwnd == 0 {
		return
	}
	enable := int32(1)
	procDwmSetWindowAttr.Call(hwnd, dwmaUseHostBackdropBrush, uintptr(unsafe.Pointer(&enable)), unsafe.Sizeof(enable))

	// Composition needs a dispatcher queue on this thread.
	options := struct{ Size, ThreadType, ApartmentType uint32 }{12, dqThreadCurrent, 0}
	var controller *comObject
	procCreateDispatcherQ.Call(uintptr(unsafe.Pointer(&options)), uintptr(unsafe.Pointer(&controller)))

	name, _ := windows.UTF16FromString("Windows.UI.Composition.Compositor")
	var hstring uintptr
	if r, _, _ := procWindowsCreateStr.Call(uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)-1), uintptr(unsafe.Pointer(&hstring))); int32(r) < 0 {
		return
	}
	defer procWindowsDeleteStr.Call(hstring)
	var inspectable *comObject
	if r, _, _ := procRoActivate.Call(hstring, uintptr(unsafe.Pointer(&inspectable))); int32(r) < 0 || inspectable == nil {
		return
	}
	compositor := inspectable.query(&iidCompositor)
	compositor3 := inspectable.query(&iidCompositor3)
	compositor7 := inspectable.query(&iidCompositor7)
	interop := inspectable.query(&iidDesktopInterop)
	if compositor == nil || compositor3 == nil || compositor7 == nil || interop == nil {
		return
	}

	target := interop.get(slotCreateDesktopTarget, hwnd, 0)
	if target == nil {
		return
	}
	container := compositor.get(slotCreateContainerVisual)
	if container == nil {
		return
	}
	root := container.query(&iidVisual)
	containerVisual := container.query(&iidContainerVisual)
	compositionTarget := target.query(&iidCompositionTarget)
	backdropBrush := compositor3.get(slotCreateHostBackdropBrush)
	if root == nil || containerVisual == nil || compositionTarget == nil || backdropBrush == nil {
		return
	}
	compositionTarget.call(slotPutRoot, uintptr(unsafe.Pointer(root)))
	backdrop.children = containerVisual.get(slotGetChildren)
	backdrop.brush = backdropBrush.query(&iidCompositionBrush)
	if backdrop.children == nil || backdrop.brush == nil {
		return
	}
	backdrop.compositor, backdrop.clips = compositor, compositor7
	backdrop.hwnd = hwnd
}

// newCard adds a visual for one more card to the tree.
func newCard() (cardVisual, bool) {
	sprite := backdrop.compositor.get(slotCreateSpriteVisual)
	if sprite == nil {
		return cardVisual{}, false
	}
	sprite.call(slotPutBrush, uintptr(unsafe.Pointer(backdrop.brush)))
	visual := sprite.query(&iidVisual)
	clip := backdrop.clips.get(slotCreateRectangleClip)
	if visual == nil || clip == nil {
		return cardVisual{}, false
	}
	if c := clip.query(&iidCompositionClip); c != nil {
		visual.call(slotPutClip, uintptr(unsafe.Pointer(c)))
	}
	backdrop.children.call(slotInsertAtTop, uintptr(unsafe.Pointer(visual)))
	return cardVisual{visual: visual, clip: clip}, true
}

// shapeCards lays a visual over each card, in physical pixels, and hides
// the visuals left over.
func shapeCards() {
	if backdrop.hwnd == 0 {
		return
	}
	scale := 1.0
	if dpi, _, _ := procGetDpiForWindow.Call(backdrop.main); dpi != 0 {
		scale = float64(dpi) / 96
	}
	for len(backdrop.cards) < len(backdrop.shapes) {
		card, ok := newCard()
		if !ok {
			break
		}
		backdrop.cards = append(backdrop.cards, card)
	}
	for i, card := range backdrop.cards {
		if i >= len(backdrop.shapes) {
			card.visual.call(slotPutIsVisible, 0)
			continue
		}
		s := backdrop.shapes[i]
		w, h, r := s.Width*scale, s.Height*scale, s.Radius*scale
		offset := [3]float32{float32(s.X * scale), float32(s.Y * scale), 0}
		card.visual.call(slotPutOffset, uintptr(unsafe.Pointer(&offset))) // a Vector3 goes by reference
		card.visual.call(slotPutSize, vector2(w, h))
		card.clip.call(slotPutLeft, f32(0))
		card.clip.call(slotPutTop, f32(0))
		card.clip.call(slotPutRight, f32(w))
		card.clip.call(slotPutBottom, f32(h))
		for _, slot := range []int{slotPutTopLeftRadius, slotPutTopRightRadius, slotPutBottomRightRadius, slotPutBottomLeftRadius} {
			card.clip.call(slot, vector2(r, r))
		}
		card.visual.call(slotPutIsVisible, 1)
	}
}

// placeBackdrop puts the blur window right under the main window, at the
// same place and size, while the main window shows and there is something
// to blur.
func placeBackdrop() {
	if backdrop.hwnd == 0 {
		return
	}
	visible, _, _ := procIsWindowVisible.Call(backdrop.main)
	if visible == 0 || len(backdrop.shapes) == 0 {
		procShowWindow.Call(backdrop.hwnd, swHide)
		return
	}
	var r struct{ Left, Top, Right, Bottom int32 }
	procGetWindowRect.Call(backdrop.main, uintptr(unsafe.Pointer(&r)))
	procSetWindowPos.Call(backdrop.hwnd, backdrop.main,
		uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top),
		swpNoActivate|swpShowWindow|swpNoOwnerZOrder)
}
