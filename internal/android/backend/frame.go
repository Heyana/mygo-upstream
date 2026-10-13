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
		b.deliver(platform.SurfaceEvent{Kind: platform.SurfaceFrame})
	})
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
