package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

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

// Moves the window to the center of its screen if it is within threshold
// points of it.
static void cmdcmdSnapWindow(void *nsWindow, int threshold) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *window = (NSWindow *)nsWindow;
		if (window == nil || window.screen == nil) return;
		NSRect area = window.screen.visibleFrame, frame = window.frame;
		NSPoint center = NSMakePoint(round(NSMidX(area) - frame.size.width / 2.0),
		                             round(NSMidY(area) - frame.size.height / 2.0));
		CGFloat dx = frame.origin.x - center.x, dy = frame.origin.y - center.y;
		if ((dx != 0 || dy != 0) && fabs(dx) <= threshold && fabs(dy) <= threshold) {
			[window setFrameOrigin:center];
		}
	});
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

// snapWindow centers the window on its screen if it is close to centered.
func snapWindow(window *application.WebviewWindow, threshold int) {
	C.cmdcmdSnapWindow(window.NativeWindow(), C.int(threshold))
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
