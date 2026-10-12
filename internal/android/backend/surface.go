//go:build android && cgo

package backend

import (
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/transfer"
)

// Surface is the platform.Surface of the Activity's window.
//
// Android has no mygo window to hang a surface off: the Activity owns the
// window and its SurfaceView hands us a Surface, which arrives through
// nativeOnSurfaceCreated and whose size arrives through
// nativeOnSurfaceChanged. So this Surface exists from the first
// surfaceCreated and is driven by the JNI callbacks rather than by a
// platform.Window (see Backend).
type Surface struct {
	b   *Backend
	egl *eglState

	// window is the ANativeWindow* from ANativeWindow_fromSurface, which
	// the backend owns and releases after the EGLSurface is gone.
	window unsafe.Pointer

	// width and height are the surface's size in device pixels; dpi is
	// its density in dots per inch, where 160 (mdpi) is Android's
	// baseline.
	width, height, dpi int
}

// Native returns the native objects a GPU renderer draws into.
//
// platform.SurfaceNative has a field per desktop platform — Windows' HWND,
// macOS's View and Layer, Linux's Widget and GLArea — and none for
// Android, where the object is an ANativeWindow* the Activity gave us
// rather than one mygo created. Adding a field would change a struct every
// backend reads, so the ANativeWindow* travels in View (as a uintptr), the
// field whose meaning — the object a renderer records drawing into — is
// the closest, and the choice is repeated here in case a dedicated field is
// wanted later.
func (s *Surface) Native() platform.SurfaceNative {
	return platform.SurfaceNative{View: uintptr(s.window)}
}

// Size returns the drawing area in DIPs and how many device pixels a DIP
// is. Android reports the surface in device pixels and reports the density
// in dots per inch, so a DIP is dpi/160 device pixels.
func (s *Surface) Size() (width, height, scale float64) {
	scale = float64(s.dpi) / 160
	if scale <= 0 {
		scale = 1
	}
	return float64(s.width) / scale, float64(s.height) / scale, scale
}

// PresentPixels shows a frame drawn in memory: premultiplied BGRA rows of
// stride bytes, width×height device pixels.
//
// The skeleton does not upload pix yet: it clears the surface and swaps,
// which is what proves the EGL surface is alive and a frame reaches the
// display. Uploading the rows — a texture and a blit, the way the Linux
// surface does — is the next step and belongs here, where the rows can
// become a GL object the EGL context keeps current.
func (s *Surface) PresentPixels(pix []byte, stride, width, height int) {
	if s.egl == nil || !s.egl.makeCurrent() {
		return
	}
	if width > 0 && height > 0 {
		s.egl.viewport(width, height)
	}
	// An opaque fill: the surface is opaque at this stage, so the alpha is
	// 1 and the color is only a placeholder for a real frame.
	s.egl.clear(0.11, 0.12, 0.16, 1)
	s.egl.swap()
}

// RequestFrame does nothing yet: Android paces frames with the
// Choreographer the Activity owns, so until the JNI layer reports frames
// the backend draws when Java reports the surface (Backend.OnSurfaceChanged).
func (s *Surface) RequestFrame() {}

// RefreshRate returns 0: the skeleton does not ask the display its rate
// yet, and 0 means unknown in this interface.
func (s *Surface) RefreshRate() float64 { return 0 }

// SetCursor does nothing: touch devices show no pointer over the surface.
func (s *Surface) SetCursor(platform.Cursor) {}

// SetTextInput does nothing: without an input method wired through JNI
// there is nothing to tell it where the caret is.
func (s *Surface) SetTextInput(platform.TextInputState) {}

// UpdateAccessibility does nothing: the accessibility bridge is not wired
// on Android yet.
func (s *Surface) UpdateAccessibility(*platform.AccessTree) {}

// StartDataDrag does nothing: native drag-and-drop does not exist here.
func (s *Surface) StartDataDrag(platform.DragRequest) {}

// CancelDataDrag does nothing: no drag can have been started.
func (s *Surface) CancelDataDrag() {}

// SetDropFormats does nothing: files are not dropped on a touch surface.
func (s *Surface) SetDropFormats([]transfer.Format) {}

// NewWebView reports that the surface hosts no web view: Android's page is
// WebView, a view of the Activity, not a web view under this surface.
func (s *Surface) NewWebView(*platform.WindowOptions, platform.WindowHandler) (platform.WebView, error) {
	return nil, platform.ErrUnsupported
}

// PlaceWebViews does nothing: there are no web views to place.
func (s *Surface) PlaceWebViews([]platform.WebViewPlacement) {}

// Surface must satisfy the platform contract.
var _ platform.Surface = (*Surface)(nil)
