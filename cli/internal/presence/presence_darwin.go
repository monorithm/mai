//go:build darwin && cgo && !presencefake

package presence

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework LocalAuthentication -framework Foundation
#include <stdlib.h>
#import <Foundation/Foundation.h>
#import <LocalAuthentication/LocalAuthentication.h>

// maiCanEvaluate returns 1 when the device owner check is available. It shows no prompt.
static int maiCanEvaluate(void) {
	@autoreleasepool {
		LAContext *ctx = [[LAContext alloc] init];
		NSError *err = nil;
		return [ctx canEvaluatePolicy:LAPolicyDeviceOwnerAuthentication error:&err] ? 1 : 0;
	}
}

// maiEvaluate shows the prompt of macOS and waits for the answer. It returns 0 when the
// user agrees, 1 when the user refuses or the check fails, and 2 when the check is not
// available.
static int maiEvaluate(const char *reason) {
	@autoreleasepool {
		LAContext *ctx = [[LAContext alloc] init];
		NSError *err = nil;
		if (![ctx canEvaluatePolicy:LAPolicyDeviceOwnerAuthentication error:&err]) {
			return 2;
		}
		NSString *text = [NSString stringWithUTF8String:reason];
		if (text == nil || text.length == 0) {
			text = @"give a mail session to an agent";
		}
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		__block int result = 1;
		[ctx evaluatePolicy:LAPolicyDeviceOwnerAuthentication
		    localizedReason:text
		              reply:^(BOOL ok, NSError *e) {
			result = ok ? 0 : 1;
			dispatch_semaphore_signal(done);
		}];
		dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
		return result;
	}
}
*/
import "C"

import "unsafe"

// check uses LocalAuthentication: Touch ID, or the password of the user.
func check(reason string) error {
	cr := C.CString(reason)
	defer C.free(unsafe.Pointer(cr))
	switch C.maiEvaluate(cr) {
	case 0:
		return nil
	case 2:
		return ErrUnavailable
	default:
		return ErrRefused
	}
}

func available() bool { return C.maiCanEvaluate() == 1 }
