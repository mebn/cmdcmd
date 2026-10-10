package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// Returns the CGDirectDisplayID of the screen under the mouse, the same ID
// Wails uses for Screen.ID, or 0 if none matches.
static unsigned int cmdcmdCursorDisplayID(void) {
	NSPoint mouse = [NSEvent mouseLocation];
	for (NSScreen *screen in [NSScreen screens]) {
		if (NSMouseInRect(mouse, screen.frame, NO)) {
			return [[screen.deviceDescription objectForKey:@"NSScreenNumber"] unsignedIntValue];
		}
	}
	return 0;
}
*/
import "C"

import (
	"strconv"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// cursorScreenID returns the Wails screen ID of the display under the cursor.
func cursorScreenID() string {
	id := application.InvokeSyncWithResult(func() C.uint { return C.cmdcmdCursorDisplayID() })
	if id == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(id), 10)
}
