package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

typedef struct { double x, y, w, h, r; } cmdcmdShape;

static NSVisualEffectView *cmdcmdBlurView;

// Blurs what is behind the window inside the given rounded rectangles: a
// visual effect view under the web view, masked to the shapes.
static void cmdcmdSetBlur(void *nsWindow, cmdcmdShape *shapes, int n) {
	NSData *data = [NSData dataWithBytes:shapes length:sizeof(cmdcmdShape) * n];
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *window = (NSWindow *)nsWindow;
		NSView *content = window.contentView;
		if (content == nil) return;
		if (cmdcmdBlurView == nil) {
			cmdcmdBlurView = [[NSVisualEffectView alloc] initWithFrame:content.bounds];
			cmdcmdBlurView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
			cmdcmdBlurView.material = NSVisualEffectMaterialPopover;
			cmdcmdBlurView.blendingMode = NSVisualEffectBlendingModeBehindWindow;
			// The panel never becomes key, so keep the effect on regardless.
			cmdcmdBlurView.state = NSVisualEffectStateActive;
			[content addSubview:cmdcmdBlurView positioned:NSWindowBelow relativeTo:nil];
		}
		int count = (int)(data.length / sizeof(cmdcmdShape));
		cmdcmdBlurView.hidden = count == 0;
		if (count == 0) return;
		cmdcmdBlurView.maskImage = [NSImage imageWithSize:content.bounds.size flipped:YES
		                                   drawingHandler:^BOOL(NSRect rect) {
			const cmdcmdShape *s = data.bytes;
			[[NSColor blackColor] set];
			for (int i = 0; i < count; i++) {
				[[NSBezierPath bezierPathWithRoundedRect:NSMakeRect(s[i].x, s[i].y, s[i].w, s[i].h)
				                                 xRadius:s[i].r yRadius:s[i].r] fill];
			}
			return YES;
		}];
	});
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

func setBlur(window *application.WebviewWindow, shapes []BlurShape) {
	cs := make([]C.cmdcmdShape, len(shapes)+1) // +1 so &cs[0] is valid when empty
	for i, s := range shapes {
		cs[i] = C.cmdcmdShape{C.double(s.X), C.double(s.Y), C.double(s.Width), C.double(s.Height), C.double(s.Radius)}
	}
	C.cmdcmdSetBlur(window.NativeWindow(), &cs[0], C.int(len(shapes)))
}
