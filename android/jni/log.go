//go:build android && cgo

package jni

/*
#include <android/log.h>
#include <stdlib.h>

static void mygo_write_log(const char *tag, const char *s) {
	__android_log_print(ANDROID_LOG_INFO, tag, "%s", s);
}
*/
import "C"

import (
	"unsafe"
)

// LogTag is the tag every line this library writes to logcat carries: the
// Java host's events, the backend's reactions, and anything an app routes
// through Log.
const LogTag = "mygo"

// Log writes one line to logcat, under LogTag. The Java host reaches it
// through the backend; an app can point its own log output here too, which is
// what makes an app's diagnostics visible at all: Go's standard log and
// println write to stderr, and on Android stderr goes nowhere a developer can
// read. `adb logcat -s mygo:V` shows these lines.
//
// Safe from any thread, Go's or cgo's: CString allocates through malloc (the
// C side owns the pointer, so no Go pointer crosses the boundary) and
// __android_log_print does not re-enter the Go runtime.
func Log(line string) {
	ctag := C.CString(LogTag)
	cline := C.CString(line)
	C.mygo_write_log(ctag, cline)
	C.free(unsafe.Pointer(ctag))
	C.free(unsafe.Pointer(cline))
}
