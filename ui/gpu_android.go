//go:build android

package ui

import (
	"github.com/egoist/mygo/internal/gpu/gl"
	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/scene"
)

// gpu_android.go is the Android window's GPU renderer: the same OpenGL ES 3
// renderer Linux runs, on the EGL context the Android backend made current,
// plus the swap EGL leaves to whoever drew.
//
// 🔴 What this file does NOT do is make a context. The backend
// (internal/android/backend) owns EGL: it makes the display, the config, the
// context and the window surface, and it makes the surface current on the
// pump goroutine — the one gl.New runs on, because every frame is drawn
// there. So the context this renderer draws in is already current, and all
// this has to do is ask for a renderer.
//
// That split is why both platforms can share 1200 lines of renderer
// (internal/gpu/gl/gl.go) and differ only in where the entry points come
// from (load_linux.go: libepoxy, load_android.go: eglGetProcAddress).
//
// Until this file existed, ui/gpu_other.go answered for Android with "no GPU
// renderer for this platform yet", the window fell through to drawing in
// memory (ui/window.go), and every frame copied the whole 1440x3168 frame
// from the CPU to the window: about 10 ms a frame, which is what made a
// flagship phone look slow.
func newGPURenderer(n platform.SurfaceNative) (gpuRenderer, error) {
	if n.View == 0 {
		// No ANativeWindow: the surface is not there yet, and the window
		// draws in memory until the next frame tries again.
		return nil, nil
	}
	r, err := gl.New()
	if err != nil {
		return nil, err
	}
	return &androidGPU{gl: r, surface: n.Present}, nil
}

// The renderer draws into the framebuffer the EGL surface binds, which is
// the default one — nothing here asks for a framebuffer of its own.
//
// androidGPU makes the swap that EGL leaves to the drawing code.
type androidGPU struct {
	gl *gl.Renderer
	// surface shows the frames, when the backend handed it over. EGL's own
	// eglSwapBuffers is reached through it (platform.FramePresenter).
	surface platform.Surface
}

// Render draws the frame and has it shown.
//
// The second half is the part EGL makes necessary: gl.Render leaves a
// finished frame in the surface's back buffer and stops there, because on
// Linux the GtkGLArea swaps when its render signal returns. Nothing swaps
// for an EGLSurface, so a renderer that does not ask is a renderer whose
// frames never reach the screen — the surface keeps showing whatever was
// swapped last, which for this app was the red-and-green "nothing has
// drawn" landmark PresentPixels paints.
func (g *androidGPU) Render(s *scene.Scene) error {
	if err := g.gl.Render(s); err != nil {
		return err
	}
	if !gpuNoSwap {
		if p, ok := g.surface.(platform.FramePresenter); ok {
			p.SetPresented()
		}
	}
	return nil
}

func (g *androidGPU) Release() { g.gl.Release() }

// gpuNoSwap skips the swap, to measure what it costs. A measurement switch,
// edited by hand: there is no runtime one because nothing but a measurement
// wants it.
var gpuNoSwap = false
