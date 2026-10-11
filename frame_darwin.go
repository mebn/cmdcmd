package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>

// Centers the window, sized width x height, on the screen under the cursor.
// Unless recenter is set, a window already on that screen stays where it is.
static void cmdcmdPlaceWindow(void *nsWindow, int width, int height, int recenter) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *window = (NSWindow *)nsWindow;
		if (window == nil) return;

		NSScreen *screen = [NSScreen mainScreen];
		NSPoint mouse = [NSEvent mouseLocation];
		for (NSScreen *s in [NSScreen screens]) {
			if (NSMouseInRect(mouse, s.frame, NO)) { screen = s; break; }
		}
		if (!recenter && window.screen == screen) return;
		NSRect area = screen.visibleFrame;
		[window setFrame:NSMakeRect(round(NSMidX(area) - width / 2.0), round(NSMidY(area) - height / 2.0), width, height)
		         display:YES];
	});
}

static int cmdcmdSnapThreshold;

// Replaces the drag Wails starts on empty space. AppKit's own window drag
// moves the window by itself until the mouse is released, so instead follow
// the mouse here and hold the window at the center of the screen under it
// whenever it comes within cmdcmdSnapThreshold points of it.
static void cmdcmdDrag(id self, SEL _cmd, NSWindow *window) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSPoint start = [NSEvent mouseLocation], origin = window.frame.origin;
		NSSize size = window.frame.size;
		// Only drags are taken off the queue; the mouse-up is left for the
		// web view, which waits for it to end the drag.
		while ([NSEvent pressedMouseButtons] & 1) {
			[NSApp nextEventMatchingMask:NSEventMaskLeftMouseDragged
			                   untilDate:[NSDate dateWithTimeIntervalSinceNow:0.02]
			                      inMode:NSEventTrackingRunLoopMode
			                     dequeue:YES];
			NSPoint mouse = [NSEvent mouseLocation];
			NSPoint to = NSMakePoint(origin.x + mouse.x - start.x, origin.y + mouse.y - start.y);
			NSScreen *screen = window.screen;
			for (NSScreen *s in [NSScreen screens]) {
				if (NSMouseInRect(mouse, s.frame, NO)) { screen = s; break; }
			}
			if (screen != nil) {
				NSRect area = screen.visibleFrame;
				NSPoint center = NSMakePoint(round(NSMidX(area) - size.width / 2.0),
				                             round(NSMidY(area) - size.height / 2.0));
				if (fabs(to.x - center.x) <= cmdcmdSnapThreshold && fabs(to.y - center.y) <= cmdcmdSnapThreshold) {
					to = center;
				}
				// Keep the top edge below the menu bar, as AppKit's drag does.
				to.y = MIN(to.y, NSMaxY(area) - size.height);
			}
			if (!NSEqualPoints(to, window.frame.origin)) [window setFrameOrigin:to];
		}
	});
}

static void cmdcmdSnapToCenter(int threshold) {
	cmdcmdSnapThreshold = threshold;
	Class delegate = objc_getClass("WebviewWindowDelegate");
	if (delegate != nil) {
		class_replaceMethod(delegate, @selector(startDrag:), (IMP)cmdcmdDrag, "v@:@");
	}
}

// Drops the window to the normal level while a dialog is up, so the dialog
// (a normal-level window) comes up on top of it, and raises it again after.
static void cmdcmdLowerWindow(void *nsWindow, int lowered) {
	dispatch_sync(dispatch_get_main_queue(), ^{
		NSWindow *window = (NSWindow *)nsWindow;
		if (window == nil) return;
		window.level = lowered ? NSNormalWindowLevel : NSFloatingWindowLevel;
	});
}

// Makes cmdcmd the active app so a dialog it opens comes up in front and
// takes the keyboard; the panel alone never activates it.
static void cmdcmdActivate(void) {
	dispatch_sync(dispatch_get_main_queue(), ^{
		[NSApp activateIgnoringOtherApps:YES];
	});
}

// Hands the keyboard back to the app you were in, if a dialog made cmdcmd
// the active app.
static void cmdcmdDeactivate(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (NSApp.isActive) [NSApp deactivate];
	});
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// placeWindow centers the window on the screen under the cursor. Unless
// recenter is set, a window already on that screen stays where it is.
func placeWindow(_ *application.App, window *application.WebviewWindow, width, height int, recenter bool) {
	r := 0
	if recenter {
		r = 1
	}
	C.cmdcmdPlaceWindow(window.NativeWindow(), C.int(width), C.int(height), C.int(r))
}

// snapToCenter makes a window dragged to within threshold points of the
// center of its screen snap there while it is still being dragged.
func snapToCenter(_ *application.WebviewWindow, threshold int) {
	C.cmdcmdSnapToCenter(C.int(threshold))
}

// lowerWindow lets a dialog cmdcmd opens lie on top of the window; it is
// floating otherwise.
func lowerWindow(window *application.WebviewWindow, lowered bool) {
	l := 0
	if lowered {
		l = 1
	}
	C.cmdcmdLowerWindow(window.NativeWindow(), C.int(l))
}

// activateApp brings cmdcmd to the front before it opens a dialog.
func activateApp() {
	C.cmdcmdActivate()
}

// deactivateApp undoes activateApp once the window hides.
func deactivateApp() {
	C.cmdcmdDeactivate()
}
