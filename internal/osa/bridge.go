// Package osa provides in-process AppleScript execution via CGO.
// This avoids TCC issues that occur when spawning osascript as a child process,
// since the AppleScript runs in the same process and inherits TCC permissions.
package osa

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework Cocoa

#import <Foundation/Foundation.h>
#import <Cocoa/Cocoa.h>

// ExecuteAppleScript runs an AppleScript string and returns the result.
// Returns NULL on success (with result in outResult), or an error string on failure.
char* execute_applescript(const char* script, char** outResult) {
    @autoreleasepool {
        NSString* scriptStr = [NSString stringWithUTF8String:script];
        NSDictionary* errorInfo = nil;

        NSAppleScript* appleScript = [[NSAppleScript alloc] initWithSource:scriptStr];
        if (!appleScript) {
            return strdup("failed to create NSAppleScript");
        }

        NSAppleEventDescriptor* result = [appleScript executeAndReturnError:&errorInfo];

        if (errorInfo) {
            NSNumber* errorNum = [errorInfo objectForKey:NSAppleScriptErrorNumber];
            NSString* errorMsg = [errorInfo objectForKey:NSAppleScriptErrorMessage];

            NSString* fullError;
            if (errorNum && errorMsg) {
                fullError = [NSString stringWithFormat:@"AppleScript error %@: %@",
                    errorNum, errorMsg];
            } else if (errorMsg) {
                fullError = errorMsg;
            } else {
                fullError = @"unknown AppleScript error";
            }

            return strdup([fullError UTF8String]);
        }

        // Extract result string if available
        if (result && outResult) {
            NSString* resultStr = [result stringValue];
            if (resultStr) {
                *outResult = strdup([resultStr UTF8String]);
            } else {
                *outResult = NULL;
            }
        }

        return NULL; // Success
    }
}

// Check if the app has Accessibility permissions.
// Returns 1 if permitted, 0 if not.
int check_accessibility_permission() {
    @autoreleasepool {
        // AXIsProcessTrusted checks if current process has Accessibility access
        // This requires ApplicationServices framework, but we can check via System Events
        NSDictionary* options = @{(__bridge NSString*)kAXTrustedCheckOptionPrompt: @NO};
        return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options) ? 1 : 0;
    }
}

// Prompt for Accessibility permissions if not already granted.
void prompt_accessibility_permission() {
    @autoreleasepool {
        NSDictionary* options = @{(__bridge NSString*)kAXTrustedCheckOptionPrompt: @YES};
        AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options);
    }
}

*/
import "C"

// HasAccessibilityPermission checks if the app has Accessibility permissions.
func HasAccessibilityPermission() bool {
	return C.check_accessibility_permission() == 1
}

// PromptAccessibilityPermission shows the system dialog to request Accessibility access.
func PromptAccessibilityPermission() {
	C.prompt_accessibility_permission()
}
