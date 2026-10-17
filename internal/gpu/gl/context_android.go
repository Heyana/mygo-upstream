//go:build android

package gl

/*
#include <EGL/egl.h>
*/
import "C"

import "unsafe"

// currentContext names the GL context the renderer's textures belong to, so
// that a renderer whose context changed can drop them rather than use
// another context's names (gl.go:533 and :794, present.go:30).
//
// libepoxy answers this through GDK on Linux; on Android it is
// eglGetCurrentContext, which is the context of the calling thread. The
// Android backend makes its EGL surface current on the pump goroutine, which
// is the thread gl.New runs on, so the answer is the renderer's context.
//
// EGL_NO_CONTEXT is (EGLContext)0, so a thread with nothing current gives 0,
// which is what a caller here tests against.
func currentContext() uintptr {
	return uintptr(unsafe.Pointer(C.eglGetCurrentContext()))
}
