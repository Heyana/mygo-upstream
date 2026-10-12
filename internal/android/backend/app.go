//go:build android && cgo

// Package backend is the Android implementation of platform.Backend: the
// Go half of the libmygo c-shared library an Android Activity loads with
// System.loadLibrary("mygo").
//
// It lives in its own package rather than in internal/android, where the
// JNI //export functions are, because -buildmode=c-shared requires a main
// package and a main package cannot be imported: the library's entry point
// (internal/android) and the backend both need the same Backend value, so
// the Backend has to be the importable side.
//
// # Threading
//
// Android is unlike the desktop backends in one way that shapes this whole
// file: the event loop belongs to the Activity, not to us. The Activity
// owns the process' main thread, its Looper and the Choreographer, and it
// calls into Go through the //export functions of package main whenever it
// wants to (surface created/changed/destroyed, touch, key, size). So this
// backend never runs an event loop of its own and never pumps a native
// queue: it parks a single goroutine, the pump, on a channel of work
// items, and every Go call that touches backend state — and, once package
// ui is wired in, every call into package ui — happens on that goroutine,
// where IsMainThread is true.
//
// The //export functions run on whatever thread Java called them on. They
// must not touch Go state there: a cgo callback that enters the scheduler
// and then blocks on the main-thread machinery deadlocks. They only call
// Post (OnSurfaceCreated, OnSurfaceChanged, ...), which hands the work to
// the pump.
package backend

import (
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/unsupported"
)

// Backend is the Android implementation of platform.Backend.
//
// It embeds internal/unsupported's Backend, which answers every method the
// skeleton does not: menus, trays, dialogs, notifications, global
// shortcuts and window creation are all the stubs a platform without a
// native implementation gets, exactly as internal/browser embeds them.
// The Activity owns the window and hands us a Surface; mygo's own windows
// are not how an Android app is shown, so NewWindow stays unsupported.
type Backend struct {
	*unsupported.Backend

	mu sync.Mutex
	h  platform.AppHandler

	// tasks carries work to the pump goroutine. It is buffered so the
	// //export functions, which run on Java's threads, rarely block.
	tasks chan func()

	// started is true once a pump goroutine exists; running, while it is
	// inside its loop.
	started atomic.Bool
	running atomic.Bool

	// threadID is the OS thread the pump is locked to (see IsMainThread).
	threadID atomic.Int64

	// signaled coalesces Signal until the pump runs the Dispatch it asks
	// for.
	signaled atomic.Bool

	// readyFired guards the single AppHandler.Ready call.
	readyFired atomic.Bool

	// quit is closed by Quit; the pump returns on it.
	quit     chan struct{}
	quitOnce sync.Once

	// wake carries Wake to a Step that is blocked.
	wake chan struct{}

	// egl is the EGL state of the process: the display and the context
	// are made once and kept across surface changes (see egl.go).
	egl *eglState
	// surface is the drawing surface of the Activity's window; it exists
	// from the first surfaceCreated until the backend quits.
	surface *Surface
}

// New creates the Android backend.
func New() *Backend {
	return &Backend{
		Backend: unsupported.New(),
		tasks:   make(chan func(), 64),
		quit:    make(chan struct{}),
		wake:    make(chan struct{}, 1),
	}
}

// Name identifies the backend.
func (b *Backend) Name() string { return "android/egl" }

// Init records the app handler and the app name.
//
// There is no native application to prepare here: Android started the
// process and its Activity before the library was loaded, so Init only
// remembers what to call back and records the thread it runs on. mygo calls
// it on the main goroutine, which is the goroutine that goes on to call
// Run, so IsMainThread answers true for the caller before the pump's first
// iteration.
func (b *Backend) Init(h platform.AppHandler, opts platform.AppOptions) error {
	b.mu.Lock()
	b.h = h
	b.mu.Unlock()
	b.threadID.Store(int64(syscall.Gettid()))
	// A pump may already have started, when Java drove the library before
	// mygo's App.Run (see Post): let it fire Ready now that there is a
	// handler to receive it.
	if b.running.Load() {
		b.Post(b.markReady)
	}
	_ = opts // the name is only used by the desktop backends so far
	return nil
}

// Run runs the pump until Quit and blocks, as platform.Backend asks.
//
// It does not create an event loop: Run is the pump, a goroutine parked on
// the work queue the JNI callbacks fill, so the loop it blocks on is the
// one driven by the Java callbacks. Run fires AppHandler.Ready once, so
// the app's windows can be opened, and then runs queued work until Quit.
//
// Nothing calls Run in this skeleton: the library's Java host drives the
// backend through the //export functions, whose first Post starts the pump
// itself. When mygo's App.Run is used instead, it calls Init and then Run,
// and Run is the pump on the main goroutine.
func (b *Backend) Run() error {
	if !b.started.CompareAndSwap(false, true) {
		// A pump already runs (Start, from Post). Wait with it.
		<-b.quit
		return nil
	}
	return b.pump()
}

// pump is the single goroutine every backend call runs on. It is locked to
// an OS thread so IsMainThread, which compares thread ids, is stable, and
// so a renderer that needs a fixed thread gets one.
func (b *Backend) pump() error {
	runtime.LockOSThread()
	b.threadID.Store(int64(syscall.Gettid()))
	b.running.Store(true)
	if b.handler() != nil {
		b.markReady()
	}
	defer func() {
		b.running.Store(false)
		b.mu.Lock()
		if b.egl != nil {
			b.egl.release()
		}
		if b.surface != nil && b.surface.window != nil {
			releaseWindow(b.surface.window)
			b.surface.window = nil
		}
		b.mu.Unlock()
	}()
	for {
		select {
		case fn := <-b.tasks:
			fn()
		case <-b.quit:
			return nil
		}
	}
}

// Post schedules fn on the pump goroutine, starting the pump when nothing
// has yet. Every entry point that runs on a Java thread goes through it;
// touching backend or ui state on the calling thread instead is the cgo
// callback deadlock this indirection exists to avoid.
func (b *Backend) Post(fn func()) {
	if b.started.CompareAndSwap(false, true) {
		go b.pump()
	}
	select {
	case b.tasks <- fn:
	case <-b.quit:
	}
}

// Quit makes Run return, as platform.Backend asks. Releasing the EGL state
// is left to the pump's exit (pump's deferred cleanup), on the thread that
// owns it.
func (b *Backend) Quit() {
	b.quitOnce.Do(func() { close(b.quit) })
}

// IsMainThread reports whether the caller is the pump goroutine, which is
// this backend's "UI thread": Android's own main thread is Java's, and no
// Go code ever runs there. Safe to call from any goroutine.
//
// Before the pump starts it reports whether the caller is the goroutine
// Init recorded — mygo's main goroutine, the one that goes on to call Run.
func (b *Backend) IsMainThread() bool {
	tid := int64(syscall.Gettid())
	if b.running.Load() {
		return b.threadID.Load() == tid
	}
	return b.started.Load() && b.threadID.Load() == tid
}

// Signal asks the backend to run AppHandler.Dispatch on the pump as soon
// as it can, as platform.Backend asks. Safe to call from any goroutine.
//
// Calls coalesce: while a Dispatch is queued, a second Signal does nothing,
// so a burst of postMain calls becomes one drain (mainLoop.drain runs
// everything queued).
func (b *Backend) Signal() {
	if !b.signaled.CompareAndSwap(false, true) {
		return
	}
	b.Post(func() {
		b.signaled.Store(false)
		if h := b.handler(); h != nil {
			h.Dispatch()
		}
	})
}

// Step blocks until Wake or Quit, or runs one queued task if one is ready,
// as platform.Backend asks. mygo calls it from await so a goroutine
// producing a value gets to run while the main thread waits. Main thread
// only.
func (b *Backend) Step() {
	select {
	case fn := <-b.tasks:
		fn()
		return
	default:
	}
	select {
	case <-b.wake:
	case <-b.quit:
	}
}

// Wake unblocks a Step that is waiting. Safe to call from any goroutine.
func (b *Backend) Wake() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// handler returns the app handler, if Init has run.
func (b *Backend) handler() platform.AppHandler {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.h
}

// markReady fires AppHandler.Ready once, on the pump.
func (b *Backend) markReady() {
	if !b.readyFired.CompareAndSwap(false, true) {
		return
	}
	if h := b.handler(); h != nil {
		h.Ready()
	}
}

// The On* methods below are what package main's //export functions call.
// Each one only queues its work: they run on the thread Java chose, and the
// pump is the only place backend state is touched.

// OnSurfaceCreated adopts the ANativeWindow of the Activity's surface and
// builds the EGLSurface for it. window is the ANativeWindow* that
// C.ANativeWindow_fromSurface returned; the backend takes it over and
// releases it in OnSurfaceDestroyed, after the EGLSurface is gone.
func (b *Backend) OnSurfaceCreated(window unsafe.Pointer) {
	if window == nil {
		return
	}
	b.Post(func() { b.surfaceCreated(window) })
}

// OnSurfaceChanged records the surface's new size and density and shows a
// frame.
func (b *Backend) OnSurfaceChanged(w, h, dpi int) {
	b.Post(func() { b.surfaceChanged(w, h, dpi) })
}

// OnSurfaceDestroyed drops the EGLSurface and the ANativeWindow reference.
// The EGL context survives (see egl.go), so coming back from the background
// keeps every GL object and does not flash a blank frame.
func (b *Backend) OnSurfaceDestroyed() {
	b.Post(func() { b.surfaceDestroyed() })
}

// OnSizeChanged records a new size without a new density, for
// nativeOnSizeChanged, which Android reports separately from the surface's
// own change.
func (b *Backend) OnSizeChanged(w, h int) {
	b.Post(func() { b.sizeChanged(w, h) })
}

// OnTouch receives a touch event. The skeleton only forwards it: there is
// no ui content to deliver a platform.SurfaceEvent to yet (that is the
// WindowHandler.SurfaceEvent path, and package ui is not wired on Android).
// Keeping the export means the Java side can send events from day one.
func (b *Backend) OnTouch(action int, x, y float32, eventTimeMs int64) {
	b.Post(func() { b.touch(action, x, y, eventTimeMs) })
}

// OnKey receives a key event, forwarded exactly like OnTouch until package
// ui is wired.
func (b *Backend) OnKey(keyCode, action, metaState int) {
	b.Post(func() { b.key(keyCode, action, metaState) })
}

// surfaceCreated (pump) makes the EGL display and context the first time
// and only ever (re)builds the EGLSurface, which is the whole point of the
// split: the Activity destroys and recreates its Surface on every
// background/foreground switch.
func (b *Backend) surfaceCreated(window unsafe.Pointer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.egl == nil {
		b.egl = &eglState{}
	}
	if err := b.egl.init(); err != nil {
		log.Printf("mygo/android: egl: %v", err)
		return
	}
	if b.surface == nil {
		b.surface = &Surface{b: b, egl: b.egl}
	}
	b.surface.window = window
	if err := b.egl.createWindow(window); err != nil {
		log.Printf("mygo/android: egl: %v", err)
		return
	}
	// The size is not known yet (it arrives with surfaceChanged, or from
	// eglQuerySurface); the first frame is drawn there.
}

// surfaceChanged (pump) records the size and shows one frame — the frame
// that proves the pipeline end to end: EGL context, EGLSurface, clear,
// swap, display.
func (b *Backend) surfaceChanged(w, h, dpi int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.surface == nil || b.surface.window == nil {
		return
	}
	b.surface.width, b.surface.height = w, h
	if dpi > 0 {
		b.surface.dpi = dpi
	}
	b.surface.PresentPixels(nil, 0, w, h)
}

// sizeChanged (pump) is surfaceChanged without a density.
func (b *Backend) sizeChanged(w, h int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.surface == nil || b.surface.window == nil {
		return
	}
	b.surface.width, b.surface.height = w, h
	b.surface.PresentPixels(nil, 0, w, h)
}

// surfaceDestroyed (pump) drops the EGLSurface, then the ANativeWindow
// reference: the EGLSurface still points at the window until it is gone, so
// the order matters. The EGL context stays alive.
func (b *Backend) surfaceDestroyed() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.egl != nil {
		b.egl.destroyWindow()
	}
	if b.surface != nil && b.surface.window != nil {
		releaseWindow(b.surface.window)
		b.surface.window = nil
	}
}

// touch (pump) is where a touch would become a platform.SurfaceEvent once
// package ui is wired; it is a no-op stub until then.
func (b *Backend) touch(action int, x, y float32, eventTimeMs int64) {
	_, _, _, _ = action, x, y, eventTimeMs
}

// key (pump) is where a key would become a platform.SurfaceEvent once
// package ui is wired; it is a no-op stub until then.
func (b *Backend) key(keyCode, action, metaState int) {
	_, _, _ = keyCode, action, metaState
}

// Backend must satisfy the platform contract.
var _ platform.Backend = (*Backend)(nil)
