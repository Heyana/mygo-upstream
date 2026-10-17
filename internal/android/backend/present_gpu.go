//go:build android && cgo

package backend

import "C"

// presentGPU shows the frame the GPU renderer drew into the EGL surface's
// back buffer: a swap, and nothing else.
//
// 🔴 Why this exists and why PresentPixels is not it. On both paths the
// ui package draws a frame and the surface shows it, but the frame gets to
// the screen differently:
//
//   - Drawn in memory: the frame is pixels on the CPU, which PresentPixels
//     uploads as a texture and draws — the swap is at the end of that.
//   - Drawn on the GPU: the renderer draws into the EGL surface's own back
//     buffer, and the only thing between it and the screen is
//     eglSwapBuffers. PresentPixels cannot do it: handed no frame it draws
//     its red-and-green "nothing has drawn" landmark, which is meant for a
//     surface that has never shown a frame, not for one holding a finished
//     one.
//
// Without this, the GPU path rendered every frame correctly and the screen
// kept showing the landmark from the last frame that went through
// PresentPixels — a red-and-green split, forever.
//
// It is safe on a surface with no EGLSurface yet: makeCurrent says so and
// nothing is swapped.
func (s *Surface) presentGPU() {
	if s.egl == nil || !s.egl.makeCurrent() {
		return
	}
	s.egl.swap()
}

// SetPresented tells the surface a frame drawn on the GPU is waiting in the
// back buffer, so the next SurfaceFrame event swaps it out. The ui package
// calls it through the FramePresenter interface (ui/gpu_android.go) after
// the renderer's Render returns.
//
// Exported for that interface: platform.Surface has no method for this
// because no other platform needs one — GTK swaps for its GtkGLArea, and a
// Windows swap chain presents on its own schedule. EGL is the one that
// leaves it to the drawing code.
func (s *Surface) SetPresented() { s.presentGPU() }
