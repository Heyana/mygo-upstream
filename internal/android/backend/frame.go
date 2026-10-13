//go:build android && cgo

package backend

import (
	"github.com/egoist/mygo/internal/platform"
)

// Frame pacing.
//
// 🔴 Android has no window manager to pace an app's frames. On the desktop a
// RequestFrame asks the compositor to call back when the display is ready to
// show one; here the request has to become a SurfaceFrame event the backend
// delivers itself, or nothing is ever drawn — the content draws frames only
// when it is asked (see platform.Surface.RequestFrame's contract: "asks for a
// SurfaceFrame event at the next opportunity"), and with no request there is
// no frame to show. The symptom has no error in it: the app comes up, the
// surface shows the placeholder fill, and no view function is ever called.
//
// AOSP's own frame clock is Choreographer, which the Activity owns. Using it
// would mean a Java-side callback per frame (and a JNI hop back for each), for
// pacing this backend then does not need: the content coalesces requests (a
// request during a frame is the next frame's), and the frame that follows is
// drawn on the pump like every other piece of work. What pacing buys —
// aligning to the display's refresh — is not free either way, and a phone that
// draws when asked is what the other backends do too (see internal/browser's
// NewWindow, which asks once and lets the content ask again).
//
// The event is delivered on the pump, once per request, and requests that
// arrive while one is already queued coalesce: a content that asks every frame
// asks once, which is the point of asking rather than drawing immediately.

// RequestFrame asks the content for a frame, as platform.Surface asks. It is
// safe from any goroutine: the request is handed to the pump, where every
// other backend call runs.
func (s *Surface) RequestFrame() {
	if s.b == nil {
		return
	}
	s.b.postFrame()
}

// postFrame queues one SurfaceFrame, unless one is already queued.
func (b *Backend) postFrame() {
	if !b.frameQueued.CompareAndSwap(false, true) {
		return
	}
	b.Post(func() {
		b.frameQueued.Store(false)
		// 🔴 The EGL context must be current before the content draws, not just
		// before it presents, and this is the only place that can guarantee it.
		//
		// Drawing a frame on the GPU is two halves that both need the context
		// current: the renderer is made at the start of the frame
		// (windowHost.makeGPU → gl.New, which reads glGetString(GL_VERSION) to
		// pick its shaders) and the frame is swapped at the end (presentGPU).
		// Presenting has always made the context current itself; making the
		// renderer did not, and relied on some earlier call having left it
		// current. That held on the first frame (surfaceChanged presents once,
		// see app.go) and broke the moment anything unbound the context:
		// surfaceDestroyed calls eglMakeCurrent(EGL_NO_SURFACE, …), so the
		// next frame made its renderer with nothing current, glGetString
		// answered "", gl.New failed with "OpenGL 0.0 is too old", and the
		// window fell back to drawing in memory — every time the app was
		// backgrounded, which is exactly the symptom this fixes.
		b.makeCurrentForFrame()
		b.deliver(platform.SurfaceEvent{Kind: platform.SurfaceFrame})
	})
}

// makeCurrentForFrame makes the surface's EGL context current, making the
// EGLSurface first if it has none, so a frame may draw and make its renderer.
// It reports nothing: a frame without a usable surface draws in memory, which
// is what the window does without a GPU.
func (b *Backend) makeCurrentForFrame() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.surface == nil || b.surface.egl == nil || b.surface.window == nil {
		return
	}
	// 🔴 The EGLSurface is retried here, not only in surfaceChanged, and this
	// is the fix for a cold start that never drew on the GPU at all.
	//
	// On this device (ColorOS) the launch sequence is surfaceCreated →
	// surfaceChanged → surfaceDestroyed inside about ten milliseconds, and
	// then nothing: the window handed to surfaceChanged is already on its way
	// out, so eglCreateWindowSurface answers EGL_BAD_ALLOC (0x3003 — the
	// window has no buffers yet, or no longer has them). A create that failed
	// once there was never retried — no further surface event comes — so the
	// app drew in memory for the whole session, and only a GPU measurement
	// would have seen it.
	//
	// A frame is the point to retry: the context has to be current then
	// anyway, a surface that finished coming up is finally usable there, and
	// frame requests coalesce, so a surface that never becomes usable costs
	// one failed call per requested frame rather than a spin.
	b.surface.egl.ensureWindow(b.surface.window)
	b.surface.egl.makeCurrent()
}

// deliver hands one event to the app's window handler, on the pump. It is a
// no-op before the app has made its window (NewWindow), which is the window
// whose handler is the one every event goes to.
func (b *Backend) deliver(ev platform.SurfaceEvent) {
	b.mu.Lock()
	w := b.win
	b.mu.Unlock()
	if w == nil {
		return
	}
	w.h.SurfaceEvent(ev)
}
