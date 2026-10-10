//go:build darwin

#include <ApplicationServices/ApplicationServices.h>

#include "hotkey_darwin.h"
#include "_cgo_export.h"

static CFMachPortRef cmdcmdTap = NULL;
static bool cmdcmdCmdDown = false;

static CGEventRef cmdcmdTapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *info) {
	if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
		CGEventTapEnable(cmdcmdTap, true);
		return event;
	}
	if (type == kCGEventKeyDown) {
		goKeyEvent(kindOther);
		return event;
	}
	if (type != kCGEventFlagsChanged) {
		return event;
	}

	CGEventFlags flags = CGEventGetFlags(event);
	int64_t keycode = CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
	bool isCmdKey = keycode == 54 || keycode == 55; // right / left Command
	bool cmd = (flags & kCGEventFlagMaskCommand) != 0;
	CGEventFlags others = flags & (kCGEventFlagMaskShift | kCGEventFlagMaskControl |
		kCGEventFlagMaskAlternate | kCGEventFlagMaskSecondaryFn | kCGEventFlagMaskAlphaShift);

	if (!isCmdKey || others) {
		goKeyEvent(kindOther);
	} else if (cmd && !cmdcmdCmdDown) {
		goKeyEvent(kindCmdDown);
	} else if (!cmd && cmdcmdCmdDown) {
		goKeyEvent(kindCmdUp);
	}
	cmdcmdCmdDown = cmd;
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
