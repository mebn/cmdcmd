//go:build darwin

#include <ApplicationServices/ApplicationServices.h>

#include "hotkey_darwin.h"
#include "_cgo_export.h"

static CFMachPortRef cmdcmdTap = NULL;

// Forwards key presses and modifier changes to Go, which decides what counts
// as a tap of the configured modifier.
static CGEventRef cmdcmdTapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *info) {
	if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
		CGEventTapEnable(cmdcmdTap, true);
	} else if (type == kCGEventKeyDown) {
		goKeyDown();
	} else if (type == kCGEventFlagsChanged) {
		goFlagsChanged((int)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode),
			(unsigned long long)CGEventGetFlags(event));
	}
	return event;
}

bool cmdcmdHasAccess(void) {
	return CGPreflightListenEventAccess();
}

void cmdcmdRequestAccess(void) {
	CGRequestListenEventAccess();
}

// cmdcmdStartTap installs a listen-only event tap on the main run loop.
// Returns false if the tap could not be created, usually because Input
// Monitoring has not been granted yet.
bool cmdcmdStartTap(void) {
	CGEventMask mask = CGEventMaskBit(kCGEventFlagsChanged) | CGEventMaskBit(kCGEventKeyDown);
	cmdcmdTap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
		kCGEventTapOptionListenOnly, mask, cmdcmdTapCallback, NULL);
	if (cmdcmdTap == NULL) {
		return false;
	}
	CFRunLoopSourceRef source = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, cmdcmdTap, 0);
	CFRunLoopAddSource(CFRunLoopGetMain(), source, kCFRunLoopCommonModes);
	CFRelease(source);
	CGEventTapEnable(cmdcmdTap, true);
	return true;
}
