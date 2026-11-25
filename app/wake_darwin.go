package app

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

extern void goScreensDidWake(void);

static void observeScreensWake(void) {
	[[[NSWorkspace sharedWorkspace] notificationCenter]
		addObserverForName:NSWorkspaceScreensDidWakeNotification
		            object:nil
		             queue:nil
		        usingBlock:^(NSNotification *note) { goScreensDidWake(); }];
}
*/
import "C"

var screensWakeHandler func()

// OnScreensWake calls fn each time the displays wake, which a full system wake
// does but a dark wake doesn't.
func OnScreensWake(fn func()) {
	screensWakeHandler = fn
	C.observeScreensWake()
}
