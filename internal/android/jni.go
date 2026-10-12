//go:build android && cgo

package main

/*
#include <jni.h>
#include <android/native_window.h>
#include <android/native_window_jni.h>

// ANativeWindow_fromSurface lives in libandroid.
#cgo LDFLAGS: -landroid
*/
import "C"

import (
	"unsafe"

	"github.com/egoist/mygo/internal/android/backend"
)

// theBackend is the library's single backend. The Java host of the app
// loads this library (System.loadLibrary("mygo")) and reaches it only
// through the //export functions below.
var theBackend = backend.New()

// The //export names are the JNI symbols Java binds to
// org.mygo.Native's static native methods, so they are spelled out by the
// Java side and must not change: Java_org_mygo_Native_<method>.
//
// Every one of them runs on whatever thread Java called it on — usually the
// Activity's main thread — and so does no Go work itself: it hands the
// event to the backend, which queues it for its pump goroutine. Touching
// backend state (and, later, package ui) on this thread would enter the Go
// scheduler from a cgo callback and then wait on the main-thread machinery,
// which deadlocks.

// Java_org_mygo_Native_nativeOnSurfaceCreated is nativeOnSurfaceCreated.
//
// It takes the ANativeWindow* of the Surface the Activity's SurfaceView
// created with ANativeWindow_fromSurface and gives it to the backend, which
// owns it from here and releases it in nativeOnSurfaceDestroyed (after the
// EGLSurface is gone). Java never computes pointers: it passes the
// android.view.Surface.
//
//export Java_org_mygo_Native_nativeOnSurfaceCreated
func Java_org_mygo_Native_nativeOnSurfaceCreated(env *C.JNIEnv, clazz C.jclass, surface C.jobject) {
	// A null Surface gives a null window, which says "nothing to adopt"
	// just as well as checking the jobject would.
	window := C.ANativeWindow_fromSurface(env, surface)
	if window == nil {
		return
	}
	theBackend.OnSurfaceCreated(unsafe.Pointer(window))
}

// Java_org_mygo_Native_nativeOnSurfaceChanged is nativeOnSurfaceChanged:
// w and h are the surface's size in device pixels, dpi its density in dots
// per inch.
//
//export Java_org_mygo_Native_nativeOnSurfaceChanged
func Java_org_mygo_Native_nativeOnSurfaceChanged(env *C.JNIEnv, clazz C.jclass, w, h, dpi C.jint) {
	theBackend.OnSurfaceChanged(int(w), int(h), int(dpi))
}

// Java_org_mygo_Native_nativeOnSurfaceDestroyed is
// nativeOnSurfaceDestroyed. It reports that the Activity lost its Surface;
// the backend keeps the EGL context and drops only the EGLSurface, so the
// app does not flash a blank frame when it comes back (see egl.go).
//
//export Java_org_mygo_Native_nativeOnSurfaceDestroyed
func Java_org_mygo_Native_nativeOnSurfaceDestroyed(env *C.JNIEnv, clazz C.jclass) {
	theBackend.OnSurfaceDestroyed()
}

// Java_org_mygo_Native_nativeOnTouch is nativeOnTouch: action is Android's
// MotionEvent action, x and y the coordinates in the surface's own pixels,
// eventTimeMs the event time that lets a gesture be reconstructed.
//
//export Java_org_mygo_Native_nativeOnTouch
func Java_org_mygo_Native_nativeOnTouch(env *C.JNIEnv, clazz C.jclass, action C.jint, x, y C.jfloat, eventTimeMs C.jlong) {
	theBackend.OnTouch(int(action), float32(x), float32(y), int64(eventTimeMs))
}

// Java_org_mygo_Native_nativeOnKey is nativeOnKey: keyCode is Android's key
// code, action its ACTION_DOWN/ACTION_UP, metaState the modifier keys held.
//
//export Java_org_mygo_Native_nativeOnKey
func Java_org_mygo_Native_nativeOnKey(env *C.JNIEnv, clazz C.jclass, keyCode, action, metaState C.jint) {
	theBackend.OnKey(int(keyCode), int(action), int(metaState))
}

// Java_org_mygo_Native_nativeOnSizeChanged is nativeOnSizeChanged: the
// view's size in device pixels, which Android reports separately from the
// surface's own change.
//
//export Java_org_mygo_Native_nativeOnSizeChanged
func Java_org_mygo_Native_nativeOnSizeChanged(env *C.JNIEnv, clazz C.jclass, w, h C.jint) {
	theBackend.OnSizeChanged(int(w), int(h))
}
