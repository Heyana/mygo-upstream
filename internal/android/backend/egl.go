//go:build android && cgo

package backend

/*
#include <EGL/egl.h>
#include <EGL/eglext.h>
#include <GLES3/gl3.h>
#include <android/native_window.h>

// libEGL and libGLESv3 are what the EGL and GL calls above resolve to on
// Android; libandroid holds ANativeWindow_release.
#cgo LDFLAGS: -lEGL -lGLESv3 -landroid
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// eglState is the EGL half of the Android surface: the display, the
// config, the context and the window surface the Activity's SurfaceView
// gives us.
//
// 🔴 Keep the display and the context; rebuild only the surface.
//
// The Activity and its Surface are two independent state machines — AOSP's
// own notes on the Android graphics architecture say so — and going to the
// background and back destroys and recreates the Surface
// (surfaceDestroyed -> surfaceCreated) while the app keeps running.
// destroyWindow below therefore tears down only the EGLSurface: the
// EGLDisplay, EGLConfig and EGLContext made by init last for the whole
// process.
//
// Rebuilding the context instead throws away every GL object, every texture
// and every uploaded resource with it, and the first frame after the switch
// is a black or white flash while the renderer makes them all again — the
// flicker this split avoids. Keeping the context also keeps those objects
// valid across the switch, which matters as soon as package ui uploads its
// textures here.
type eglState struct {
	display C.EGLDisplay
	config  C.EGLConfig
	context C.EGLContext
	surface C.EGLSurface

	initialized bool
}

// init makes the display, the config and the context, once. It is
// idempotent, so surfaceCreated can call it on every surface.
func (e *eglState) init() error {
	if e.initialized {
		return nil
	}
	display := C.eglGetDisplay(C.EGL_DEFAULT_DISPLAY)
	if display == C.EGL_NO_DISPLAY {
		return fmt.Errorf("eglGetDisplay returned no display (0x%x)", C.eglGetError())
	}
	var major, minor C.EGLint
	if C.eglInitialize(display, &major, &minor) == C.EGL_FALSE {
		return fmt.Errorf("eglInitialize failed (0x%x)", C.eglGetError())
	}
	// The config is asked for twice: the first asks for an alpha channel,
	// the second does not. A window on Android is opaque — its surface
	// holds no alpha — and a driver that has no ES3 config with alpha for
	// a window but has one without, answers the first with nothing. The
	// alpha channel is not needed to composite (the compositor reads the
	// surface as opaque), so the second ask is the one that matters, and
	// the first is kept because some devices (Samsung S9, as gio notes)
	// want it.
	//
	// Falling through to drawing in memory instead, which is what a single
	// ask did, costs about 10 ms a frame on a 1440x3168 phone: the whole
	// frame is copied from the CPU to the window.
	base := []C.EGLint{
		C.EGL_RENDERABLE_TYPE, C.EGL_OPENGL_ES3_BIT,
		C.EGL_SURFACE_TYPE, C.EGL_WINDOW_BIT,
		C.EGL_RED_SIZE, 8,
		C.EGL_GREEN_SIZE, 8,
		C.EGL_BLUE_SIZE, 8,
		C.EGL_DEPTH_SIZE, 0,
		C.EGL_STENCIL_SIZE, 0,
	}
	var config C.EGLConfig
	var n C.EGLint
	chosen := false
	for _, alpha := range []C.EGLint{8, 0} {
		attribs := append(append([]C.EGLint{}, base...), C.EGL_ALPHA_SIZE, alpha, C.EGL_NONE)
		if C.eglChooseConfig(display, &attribs[0], &config, 1, &n) == C.EGL_TRUE && n >= 1 {
			chosen = true
			break
		}
	}
	if !chosen {
		return fmt.Errorf("eglChooseConfig found no ES3 window config (0x%x)", C.eglGetError())
	}
	contextAttribs := []C.EGLint{
		C.EGL_CONTEXT_CLIENT_VERSION, 3,
		C.EGL_NONE,
	}
	context := C.eglCreateContext(display, config, C.EGL_NO_CONTEXT, &contextAttribs[0])
	if context == C.EGL_NO_CONTEXT {
		return fmt.Errorf("eglCreateContext failed (0x%x)", C.eglGetError())
	}
	e.display, e.config, e.context = display, config, context
	e.initialized = true
	return nil
}

// createWindow makes the EGLSurface for the ANativeWindow Java handed us.
// Only this is rebuilt when the Activity's surface changes: the context
// stays (see eglState). window is the ANativeWindow* from
// C.ANativeWindow_fromSurface.
func (e *eglState) createWindow(window unsafe.Pointer) error {
	if e.surface != C.EGL_NO_SURFACE {
		e.destroyWindow()
	}
	// First with no attributes, then — if that fails — with an explicit
	// empty list. The two are the same call to every driver seen so far,
	// but some answer nil and not an EGL_NO_SURFACE-terminated list (gio
	// carries the same retry, with an sRGB colorspace attribute it drops
	// on the second try; this backend asks for no colorspace at all, since
	// its frames are sRGB and the surface's default is what it wants).
	//
	// The error carries eglGetError(): "failed" alone is what this said
	// before, and it sent a whole afternoon after the wrong thing.
	surface := C.eglCreateWindowSurface(e.display, e.config, (*C.ANativeWindow)(window), nil)
	if surface == C.EGL_NO_SURFACE {
		empty := []C.EGLint{C.EGL_NONE}
		surface = C.eglCreateWindowSurface(e.display, e.config, (*C.ANativeWindow)(window), &empty[0])
	}
	if surface == C.EGL_NO_SURFACE {
		return fmt.Errorf("eglCreateWindowSurface failed (0x%x)", C.eglGetError())
	}
	e.surface = surface
	return nil
}

// destroyWindow drops the EGLSurface only; the context is kept, so
// backgrounding the Activity costs one surface, not the renderer.
func (e *eglState) destroyWindow() {
	if e.surface == C.EGL_NO_SURFACE {
		return
	}
	// Unbind the surface before destroying it, so nothing can draw into a
	// surface that is going away.
	C.eglMakeCurrent(e.display, C.EGL_NO_SURFACE, C.EGL_NO_SURFACE, C.EGL_NO_CONTEXT)
	C.eglDestroySurface(e.display, e.surface)
	e.surface = C.EGL_NO_SURFACE
}

// release tears everything down, for the end of the process.
func (e *eglState) release() {
	e.destroyWindow()
	if e.context != C.EGL_NO_CONTEXT {
		C.eglDestroyContext(e.display, e.context)
		e.context = C.EGL_NO_CONTEXT
	}
	if e.display != C.EGL_NO_DISPLAY {
		C.eglTerminate(e.display)
		e.display = C.EGL_NO_DISPLAY
	}
	e.initialized = false
}

// makeCurrent makes the surface's context current, ready to draw.
func (e *eglState) makeCurrent() bool {
	if e.surface == C.EGL_NO_SURFACE {
		return false
	}
	return C.eglMakeCurrent(e.display, e.surface, e.surface, e.context) == C.EGL_TRUE
}

// swap shows the frame that has been drawn.
func (e *eglState) swap() bool {
	return C.eglSwapBuffers(e.display, e.surface) == C.EGL_TRUE
}

// viewport sets the GL viewport to the surface's device pixels.
func (e *eglState) viewport(w, h int) {
	C.glViewport(0, 0, C.GLint(w), C.GLint(h))
}

// clear fills the surface with a color.
func (e *eglState) clear(r, g, b, a float32) {
	C.glClearColor(C.GLfloat(r), C.GLfloat(g), C.GLfloat(b), C.GLfloat(a))
	C.glClear(C.GL_COLOR_BUFFER_BIT)
}

// releaseWindow drops the reference ANativeWindow_fromSurface took. The
// backend calls it after destroyWindow, never before: the EGLSurface still
// refers to the window until it is gone.
func releaseWindow(window unsafe.Pointer) {
	if window != nil {
		C.ANativeWindow_release((*C.ANativeWindow)(window))
	}
}

// scissor limits drawing to a rectangle in device pixels, so the landmark
// fill can be two colors (see Surface.PresentPixels). Lower-left origin, as
// glScissor is.
func (e *eglState) scissor(x, y, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	C.glEnable(C.GL_SCISSOR_TEST)
	C.glScissor(C.GLint(x), C.GLint(y), C.GLsizei(w), C.GLsizei(h))
}

// scissorOff lifts the limit scissor set, so nothing later draws clipped.
func (e *eglState) scissorOff() { C.glDisable(C.GL_SCISSOR_TEST) }
