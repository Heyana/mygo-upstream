//go:build android && cgo

package backend

// Go's own printing does not reach logcat on Android: the runtime redirects
// stdout and stderr to /dev/null, so log.Printf and println are written
// nowhere. That is not an obvious thing to run into — a probe that prints
// nothing looks exactly like a probe that never ran — and it cost an hour
// of looking in the wrong place while the QR scanner was being added.
//
// logcat is a line-oriented __android_log_write call, so log's output is
// wrapped in a writer that makes one call per Write, and stdout/stderr are
// dup2'd onto a pipe drained the same way. View it with:
//
//	adb logcat mygo:V
//
// The tag is "mygo", which is what the Java side uses too, so one filter
// shows both halves of a call that crosses JNI.

/*
#include <android/log.h>
#include <stdlib.h>
*/
import "C"

import (
	"bufio"
	"log"
	"os"
	"unsafe"
)

const logTag = "mygo"

func init() {
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))
	log.SetOutput(logcatWrite{})
	go drainStderr()
	go drainStdout()
}

type logcatWrite struct{}

func (logcatWrite) Write(p []byte) (int, error) {
	// Write gets one whole line from log; a trailing newline is implied by
	// logcat, and an embedded NUL would end the C string early.
	writeLogcat(C.ANDROID_LOG_INFO, p)
	return len(p), nil
}

func writeLogcat(prio C.int, p []byte) {
	c := C.CString(string(p))
	defer C.free(unsafe.Pointer(c))
	tag := C.CString(logTag)
	defer C.free(unsafe.Pointer(tag))
	C.__android_log_write(prio, tag, c)
}

// drainStderr sends this process's stderr to logcat. The write end has to
// outlive init, or the pipe would be closed and the reader would exit.
var stderrW *os.File

func drainStderr() {
	r, w, err := os.Pipe()
	if err != nil {
		return
	}
	stderrW = w
	os.Stderr = w
	go forward(r, C.ANDROID_LOG_ERROR)
}

func drainStdout() {
	r, w, err := os.Pipe()
	if err != nil {
		return
	}
	os.Stdout = w
	go forward(r, C.ANDROID_LOG_INFO)
}

func forward(r *os.File, prio C.int) {
	const maxLine = 1024 // matches android/log.h; logcat truncates past it
	br := bufio.NewReaderSize(r, maxLine)
	for {
		line, _, err := br.ReadLine()
		if len(line) > 0 {
			writeLogcat(prio, line)
		}
		if err != nil {
			return
		}
	}
}
