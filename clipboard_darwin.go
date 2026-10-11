package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

// Returns the paths of the files on the clipboard, separated by newlines, or
// NULL if there are none. The caller frees the result.
static char *cmdcmdClipboardFiles(void) {
	__block char *out = NULL;
	dispatch_sync(dispatch_get_main_queue(), ^{
		NSArray<NSURL *> *urls = [[NSPasteboard generalPasteboard]
			readObjectsForClasses:@[[NSURL class]]
			              options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}];
		NSMutableArray<NSString *> *paths = [NSMutableArray array];
		for (NSURL *url in urls) {
			if (url.path != nil) [paths addObject:url.path];
		}
		if (paths.count > 0) out = strdup([[paths componentsJoinedByString:@"\n"] UTF8String]);
	});
	return out;
}
*/
import "C"

import (
	"strings"
	"unsafe"
)

// clipboardFiles returns the paths of the files copied to the clipboard.
func clipboardFiles() []string {
	s := C.cmdcmdClipboardFiles()
	if s == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(s))
	return strings.Split(C.GoString(s), "\n")
}
