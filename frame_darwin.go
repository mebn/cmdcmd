package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// Centers the window, sized width x height, on the screen under the cursor.
static void cmdcmdPlaceWindow(void *nsWindow, int width, int height) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *window = (NSWindow *)nsWindow;
		if (window == nil) return;

		NSScreen *screen = [NSScreen mainScreen];
		NSPoint mouse = [NSEvent mouseLocation];
		for (NSScreen *s in [NSScreen screens]) {
			if (NSMouseInRect(mouse, s.frame, NO)) { screen = s; break; }
		}
		NSRect area = screen.visibleFrame;
		[window setFrame:NSMakeRect(round(NSMidX(area) - width / 2.0), round(NSMidY(area) - height / 2.0), width, height)
		         display:YES];
	});
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// placeWindow centers the window on the screen under the cursor.
func placeWindow(_ *application.App, window *application.WebviewWindow, width, height int) {
	C.cmdcmdPlaceWindow(window.NativeWindow(), C.int(width), C.int(height))
}
