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

	// present holds the GL objects that turn a frame drawn in memory into what
	// the display shows; they belong to the EGL context, so they outlive every
	// surface change (see glpresent.go).
	present presenter

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
	return platform.SurfaceNative{View: uintptr(s.window), Present: s}
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

// SetSwapInterval tells eglSwapBuffers how many vsyncs to wait for. 1 is the
// default; 0 makes it return as soon as the buffer is handed over, which is
// how the frame's own cost is read apart from its pacing (see
// eglState.setSwapInterval).
//
// It is platform.SwapIntervalSurface, reached by an optional type assertion
// rather than through the Surface interface, because only EGL has an
// interval to set.
func (s *Surface) SetSwapInterval(n int) { s.egl.setSwapInterval(n) }

// PresentPixels shows a frame drawn in memory: premultiplied BGRA rows of
// stride bytes, width×height device pixels.
//
// The rows become a texture the presenter draws (see glpresent.go), so the
// frame the ui package rendered in memory is what the display shows. A caller
// that hands over no usable frame gets the placeholder fill instead, which
// keeps the surface from showing whatever the driver last left in the buffer.
func (s *Surface) PresentPixels(pix []byte, stride, width, height int) {
	if s.egl == nil || !s.egl.makeCurrent() {
		return
	}
	if width > 0 && height > 0 {
		s.egl.viewport(width, height)
	}
	if !s.present.draw(pix, stride, width, height) {
		// No frame to show: the app has not drawn one, or has not started.
		// The fill is a landmark, not decoration — a solid color says "the
		// surface is alive and nothing has drawn yet", which a black screen
		// cannot: "the renderer never ran" and "the app drew a black frame"
		// look identical without it. The two halves (red right, green left)
		// make that distinction readable by eye, and by a pixel probe.
		s.egl.clear(0.85, 0.15, 0.15, 1)
		if width > 0 && height > 0 {
			s.egl.scissor(0, 0, width/2, height)
			s.egl.clear(0.15, 0.75, 0.25, 1)
			s.egl.scissorOff()
		}
	}
	s.egl.swap()
}

// RefreshRate returns 0: the skeleton does not ask the display its rate
// yet, and 0 means unknown in this interface.
func (s *Surface) RefreshRate() float64 { return 0 }

// SetCursor does nothing: touch devices show no pointer over the surface.
func (s *Surface) SetCursor(platform.Cursor) {}

// SetTextInput hands the surface's text-input state to the Android IME state
// machine, which is what answers an input method's queries and raises the
// soft keyboard (see textinput.go).
func (s *Surface) SetTextInput(t platform.TextInputState) {
	theIme.SetTextInput(t)
}

// UpdateAccessibility pushes the tree to the Java node provider, which is what
// TalkBack reads (see platform.go and MygoPlatform.kt).
func (s *Surface) UpdateAccessibility(t *platform.AccessTree) { s.b.UpdateAccessibility(t) }

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
