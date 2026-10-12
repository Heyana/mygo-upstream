//go:build js && wasm

// Package browser is the backend for the web. MyGo runs its event loop on
// the page's animation frames, and a window that shows Content gets a
// surface whose frames the ui package draws straight into a canvas 2D
// context: the native user interface runs in a web page, drawn by the
// browser, sharp at any zoom.
//
// The page names the canvas with an element of id "mygo-canvas" (another id
// through the global window.mygoCanvasID), and sizes it with CSS: the
// surface takes its size from the element's box.
//
// What the browser has no equivalent of - windows to move, a menu bar, a
// tray, the clipboard of the system, file dialogs - is inert here, and the
// rest of the backend falls back to internal/unsupported.
package browser

import (
	"errors"
	"log"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/unsupported"
	"github.com/egoist/mygo/transfer"
)

// New returns the backend of the browser.
func New() platform.Backend { return &backend{Backend: unsupported.New()} }

// errNoPage is what the page methods of a window report: this backend shows
// Content, not a web page.
var errNoPage = errors.New("mygo: the browser backend shows Content, not a web page")

// defaultCanvasID is the canvas element that shows Content.
const defaultCanvasID = "mygo-canvas"

// backend runs the event loop on the page's animation frames.
type backend struct {
	*unsupported.Backend

	app  platform.AppHandler
	wins []*window

	// done closes when the application quits; Run returns then.
	done chan struct{}
	// wake carries Wake to Step.
	wake chan struct{}
	quit bool

	// frame is the animation frame callback, made once and reused; a new
	// js.Func each frame would leak.
	frame    js.Func
	dispatch js.Func
	// framePending tells that a frame callback is scheduled.
	framePending bool
	// signaled tells that a Dispatch is scheduled.
	signaled bool
}

// Init prepares the backend; the page is already loaded, so there is
// nothing to do but keep the application's handler.
func (b *backend) Init(h platform.AppHandler, _ platform.AppOptions) error {
	b.app = h
	b.done = make(chan struct{})
	b.wake = make(chan struct{}, 1)
	b.frame = js.FuncOf(b.onFrame)
	b.dispatch = js.FuncOf(b.onDispatch)
	return nil
}

// Name identifies the backend.
func (b *backend) Name() string { return "js/canvas" }

// Run runs the page until the application quits: it reports the app ready,
// runs whatever was scheduled before the loop, and then lets the page's
// animation frames drive it.
func (b *backend) Run() error {
	b.app.Ready()
	b.app.Dispatch()
	b.scheduleFrame()
	<-b.done
	return nil
}

// Quit makes Run return.
func (b *backend) Quit() {
	if b.quit {
		return
	}
	b.quit = true
	close(b.done)
}

// IsMainThread reports true: the page has one thread.
func (b *backend) IsMainThread() bool { return true }

// Signal schedules a Dispatch, on a macrotask so a callback that signals
// while running does not run its work before returning.
func (b *backend) Signal() {
	if b.signaled || b.quit {
		return
	}
	b.signaled = true
	js.Global().Call("setTimeout", b.dispatch, 0)
}

func (b *backend) onDispatch(js.Value, []js.Value) any {
	b.signaled = false
	b.app.Dispatch()
	return nil
}

// Step waits for Wake; the page still runs while it waits, as the Go
// runtime gives the JavaScript event loop control when every goroutine
// blocks.
func (b *backend) Step() { <-b.wake }

// Wake unblocks a pending Step.
func (b *backend) Wake() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// scheduleFrame asks the page for an animation frame, once.
func (b *backend) scheduleFrame() {
	if b.framePending || b.quit {
		return
	}
	b.framePending = true
	js.Global().Call("requestAnimationFrame", b.frame)
}

// onFrame draws the frames the windows asked for, runs the main loop, and
// keeps asking while any window wants more.
func (b *backend) onFrame(js.Value, []js.Value) any {
	b.framePending = false
	for _, w := range b.wins {
		if w.surface != nil {
			w.surface.tick()
		}
	}
	b.app.Dispatch()
	for _, w := range b.wins {
		if w.surface != nil && w.surface.wantsFrame() {
			b.scheduleFrame()
			break
		}
	}
	return nil
}

// NewWindow makes a window. The browser's page is the window, so there is
// nothing to create; Content gets a surface over the page's canvas.
func (b *backend) NewWindow(o *platform.WindowOptions, h platform.WindowHandler) (platform.Window, error) {
	w := &window{
		b:       b,
		h:       h,
		title:   o.Title,
		bounds:  platform.Rect{Width: o.Width, Height: o.Height},
		visible: false,
	}
	if o.Surface {
		w.surface = &surface{b: b, w: w, scale: 1}
		w.surface.attach()
	}
	b.wins = append(b.wins, w)
	// A window that has just been made asks for a frame, and this is the
	// only place that does: everything else here draws the frames the
	// content asks for and no others, so a window whose content had not
	// asked yet would never be drawn at all — and a content that draws
	// nothing until something else happens (a fetch answering, a list
	// arriving) would have nothing to ask from, having never been run.
	//
	// It is also how a browser backend signals that a window exists: off
	// the page NewWindow shows one, and on it the frame that follows is
	// what a person sees.
	if w.surface != nil {
		w.surface.RequestFrame()
	}
	return w, nil
}

// Theme reads the page's color scheme.
func (b *backend) Theme() platform.Theme { return theme{} }

// Clipboard is the page's clipboard, which is where the embedded unsupported
// backend's own would otherwise answer: that one takes nothing and gives
// nothing, and the page can do more than that. See clipboard.go.
func (b *backend) Clipboard() platform.Clipboard { return clipboard{} }

type theme struct{}

// IsDark reports prefers-color-scheme: dark.
func (theme) IsDark() bool {
	m := js.Global().Call("matchMedia", "(prefers-color-scheme: dark)")
	if m.IsUndefined() || m.IsNull() {
		return false
	}
	return m.Get("matches").Truthy()
}

func (theme) SetSource(string) {}

// UIFont is "" : text on the canvas is the browser's own, which knows its
// interface font already.
func (theme) UIFont() string { return "" }

func (theme) FontRendering() platform.FontRendering { return platform.FontRendering{} }
func (theme) Preferences() platform.Preferences     { return platform.Preferences{} }

// window is a MyGo window: the page itself. Moving, resizing and stacking
// are the browser's, so the methods that would ask a window manager do
// nothing.
type window struct {
	b       *backend
	h       platform.WindowHandler
	surface *surface

	title   string
	bounds  platform.Rect
	visible bool
	focused bool
	closed  bool
}

func (w *window) Handle() uintptr { return 1 }

func (w *window) Surface() platform.Surface {
	if w.surface == nil {
		return nil
	}
	return w.surface
}

func (w *window) SetTitle(title string) {
	w.title = title
	js.Global().Get("document").Set("title", title)
}

func (w *window) Title() string { return w.title }

func (w *window) SetBounds(r platform.Rect)        { w.bounds = r }
func (w *window) Bounds() platform.Rect            { return w.bounds }
func (w *window) SetContentBounds(r platform.Rect) { w.bounds = r }
func (w *window) ContentBounds() platform.Rect     { return w.bounds }

// Show makes the window visible; the page always is, so it just asks for a
// frame and reports the window focused.
func (w *window) Show() {
	w.visible = true
	w.askFrame()
}

func (w *window) ShowInactive() {
	w.visible = true
	w.askFrame()
}

func (w *window) Hide() {
	w.visible = false
	if w.surface != nil {
		w.surface.clear()
	}
}

func (w *window) IsVisible() bool { return w.visible }

func (w *window) Focus() {
	if !w.focused {
		w.focus()
	}
}

func (w *window) Blur() {
	if w.focused {
		w.blur()
	}
}

func (w *window) IsFocused() bool { return w.focused }

func (w *window) Close() {
	if w.closed {
		return
	}
	w.closed = true
	w.h.Closed()
}

// focus reports the window gaining the keyboard.
func (w *window) focus() {
	if w.focused {
		return
	}
	w.focused = true
	w.h.Focused()
	if w.surface != nil {
		w.surface.send(platform.SurfaceEvent{Kind: platform.SurfaceFocus})
	}
}

// blur reports the window losing the keyboard.
func (w *window) blur() {
	if !w.focused {
		return
	}
	w.focused = false
	w.h.Blurred()
	if w.surface != nil {
		w.surface.send(platform.SurfaceEvent{Kind: platform.SurfaceBlur})
	}
}

func (w *window) askFrame() {
	if w.surface != nil {
		w.surface.RequestFrame()
	}
}

// surface is the drawing area of a window: the page's canvas.
type surface struct {
	b *backend
	w *window

	mu      sync.Mutex
	pending bool
	// scale is the device pixels per DIP, measured with the size.
	scale float64
	// width and height are the drawing area in DIPs.
	width, height float64
	measured      bool
	cursor        platform.Cursor
	// clicks counts quick successive presses, for double clicks, and when
	// the last was.
	clicks int
	last   float64

	el js.Value
	// funcs holds the event listeners; the page keeps the js.Func values
	// alive through them.
	funcs []js.Func

	// text is the hidden editable element that receives text input, made on
	// the first editable element that takes the keyboard. The page has no
	// editable element of its own — a canvas is not one — and an input
	// method only composes into one, so this is the proxy the IME talks to.
	// See text_input.go.
	text js.Value
	// composing is true between compositionstart and compositionend.
	composing bool
	// input is the last state the content gave; the element is placed and
	// filled from it.
	input platform.TextInputState
	// clientComposition pairs the content's UnmarkText with the Replace that
	// follows it, as the native surfaces' own field does.
	clientComposition platform.InputComposition
	// textValue is the element's value as the content last knew it, so that
	// a change can be diffed into the range it replaced.
	textValue string
}

// canvasID is the id of the canvas element: window.mygoCanvasID when the
// page set one, the default otherwise.
func canvasID() string {
	v := js.Global().Get("mygoCanvasID")
	if v.Type() == js.TypeString && v.String() != "" {
		return v.String()
	}
	return defaultCanvasID
}

func (s *surface) element() js.Value {
	return js.Global().Get("document").Call("getElementById", canvasID())
}

// attach wires the canvas: it takes the element, listens for the page's
// input, and measures the drawing area. Main thread only.
func (s *surface) attach() {
	if !s.el.IsUndefined() {
		return
	}
	el := s.element()
	if el.IsUndefined() || el.IsNull() {
		return
	}
	s.el = el
	el.Set("tabindex", 0)
	s.listen()
	s.measure()
	// The content lays out again for the size it now knows.
	s.send(platform.SurfaceEvent{Kind: platform.SurfaceResize})
}

// references counts the page's events since the last look, which is what
// watchReferences reads to decide whether the app has taken out syscall/js
// references that nobody has handed back.
//
// A js.Value costs a slot in the runtime's JavaScript reference table, and
// that slot is what keeps the page's object alive: the event, its path, the
// element's rectangle. The slot comes back from a finalizer, and a finalizer
// needs a collection. Under wasm the runtime collects on the heap trigger and
// on nothing else: the clock trigger belongs to the system monitor, and
// runtime/proc.go says "No threads on wasm yet, so no sysmon". A page that is
// only being moused over never grows the heap, so it never collects, and
// every event the pointer has ever sent stays alive in that table. Measured
// on the wasm lab: 4334 MouseEvents with their DOMRects, held by
// globalThis.Go._values. See MYGO-WEB-REPORT.md §9.15.
var references atomic.Int64

// refCheckPeriod is how often those references are looked at.
const refCheckPeriod = time.Second

// refForceGCPeriod is the longest the app lets the heap go uncollected when
// nothing is happening. It is the runtime's own clock period, from
// runtime/proc.go forcegcperiod.
const refForceGCPeriod = 2 * time.Minute

// watchReferences is the collection the runtime would force itself if it had
// a system monitor. It runs under one rule: collect when events came in and
// the app's own allocation has not collected since the last look. A page that
// is rendering allocates past the heap trigger and collects by itself, which
// hands the references back too and is why that case is left alone; a page
// that is only being scrolled or moused over does not, and is the case this
// exists for. A page that is doing nothing at all collects once every
// refForceGCPeriod, which is what the runtime would have done.
func watchReferences() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	collections := m.NumGC
	forced := time.Now()
	for range time.Tick(refCheckPeriod) {
		runtime.ReadMemStats(&m)
		if m.NumGC != collections {
			// The app collected on its own; the events above went with it.
			collections = m.NumGC
			references.Store(0)
			forced = time.Now()
			continue
		}
		if references.Swap(0) == 0 && time.Since(forced) < refForceGCPeriod {
			continue
		}
		runtime.GC()
		runtime.ReadMemStats(&m)
		collections = m.NumGC
		forced = time.Now()
	}
}

// watchOnce starts watchReferences the first time a surface listens.
var watchOnce sync.Once

// listen registers the page's events on the canvas and the page.
func (s *surface) listen() {
	watchOnce.Do(func() { go watchReferences() })
	el := s.el
	on := func(event string, target js.Value, fn func(js.Value)) {
		f := js.FuncOf(func(_ js.Value, args []js.Value) any {
			references.Add(1)
			noteEvent()
			if len(args) > 0 {
				fn(args[0])
			}
			return nil
		})
		s.funcs = append(s.funcs, f)
		target.Call("addEventListener", event, f)
	}

	on("mousemove", el, s.mouseMove)
	on("mousedown", el, func(e js.Value) {
		// The default action of a press moves the keyboard focus to the
		// nearest focusable ancestor, which for a canvas is the body, and
		// it runs after this handler — so it takes back the focus the
		// content has just given the hidden text input, and the input
		// method never starts. The content owns the interaction; the
		// page's own is not wanted. It is the same call an editor makes to
		// stop a press from selecting the page's text.
		e.Call("preventDefault")
		s.mouseButton(e, platform.PointerDown)
	})
	on("mouseup", el, func(e js.Value) { s.mouseButton(e, platform.PointerUp) })
	on("mouseleave", el, func(js.Value) { s.send(platform.SurfaceEvent{Kind: platform.PointerLeave}) })
	on("wheel", el, s.wheel)
	on("contextmenu", el, func(e js.Value) { e.Call("preventDefault") })
	on("keydown", js.Global(), func(e js.Value) { s.key(e, platform.KeyPressed) })
	on("keyup", js.Global(), func(e js.Value) { s.key(e, platform.KeyReleased) })
	on("focus", js.Global(), func(js.Value) { s.w.focus() })
	on("blur", js.Global(), func(js.Value) { s.w.blur() })
	on("resize", js.Global(), func(js.Value) {
		s.measure()
		s.send(platform.SurfaceEvent{Kind: platform.SurfaceResize})
		s.RequestFrame()
	})
}

// point maps a page event to DIPs relative to the canvas.
func (s *surface) point(e js.Value) (float64, float64) {
	r := s.el.Call("getBoundingClientRect")
	x := e.Get("clientX").Float() - r.Get("left").Float()
	y := e.Get("clientY").Float() - r.Get("top").Float()
	return x, y
}

// mods reads the modifier keys of a page event.
func mods(e js.Value) platform.Modifiers {
	var m platform.Modifiers
	if e.Get("shiftKey").Truthy() {
		m |= platform.ModShift
	}
	if e.Get("ctrlKey").Truthy() {
		m |= platform.ModCtrl
	}
	if e.Get("altKey").Truthy() {
		m |= platform.ModAlt
	}
	if e.Get("metaKey").Truthy() {
		m |= platform.ModSuper
	}
	return m
}

func (s *surface) mouseMove(e js.Value) {
	x, y := s.point(e)
	s.send(platform.SurfaceEvent{Kind: platform.PointerMove, PointerType: platform.PointerMouse, X: x, Y: y, Mods: mods(e)})
}

func (s *surface) mouseButton(e js.Value, kind platform.SurfaceEventKind) {
	// The page has its own context menu; the content's comes from the
	// secondary button, which the backend routes like any other press.
	x, y := s.point(e)
	button := e.Get("button").Int()
	clicks := 1
	if kind == platform.PointerDown {
		now := nowMillis()
		if button == 0 && now-s.last < 400 {
			s.clicks++
		} else {
			s.clicks = 1
		}
		s.last = now
		clicks = s.clicks
	} else {
		clicks = s.clicks
	}
	s.send(platform.SurfaceEvent{
		Kind: kind, PointerType: platform.PointerMouse, X: x, Y: y, Button: button, Clicks: clicks, Mods: mods(e),
	})
}

func (s *surface) wheel(e js.Value) {
	x, y := s.point(e)
	dx := e.Get("deltaX").Float()
	dy := e.Get("deltaY").Float()
	// A page's wheel is in pixels; lines and pages are brought to pixels
	// so the content scrolls at a familiar rate.
	switch e.Get("deltaMode").Int() {
	case 1:
		dx, dy = dx*16, dy*16
	case 2:
		dx, dy = dx*s.height, dy*s.height
	}
	// A wheel event's deltas already have the sign PointerScroll takes:
	// deltaY is positive when the page is scrolled down, which is positive
	// DY, "moves the view down the content". The negations the native
	// backends do are theirs to do: a Windows notch is positive away from
	// the user and a Cocoa scrollingDelta is the gesture's own direction,
	// and neither is a DOM delta. Taking it as this file did first — the
	// negation copied from the Cocoa handler — scrolled the page backwards.
	s.send(platform.SurfaceEvent{
		Kind: platform.PointerScroll, PointerType: platform.PointerMouse, X: x, Y: y, DX: dx, DY: dy, Precise: true, Mods: mods(e),
	})
	e.Call("preventDefault")
}

// key delivers a key press or release, and the text a printable key types.
func (s *surface) key(e js.Value, kind platform.SurfaceEventKind) {
	if t := e.Get("target"); !t.IsUndefined() && !t.IsNull() {
		tag := t.Get("tagName").String()
		if tag == "INPUT" || tag == "TEXTAREA" {
			return
		}
	}
	if kind == platform.KeyReleased {
		// The page reports a key up for shortcuts too; the content only
		// needs the ones it saw go down.
		if k := keyFor(e); k != platform.KeyUnknown {
			s.send(platform.SurfaceEvent{Kind: kind, Key: k, Mods: mods(e)})
		}
		return
	}
	k := keyFor(e)
	m := mods(e)
	if k == platform.KeyUnknown {
		return
	}
	if platform.KeyTab == k || (m == 0 && (k == platform.KeySpace || k == platform.KeyUp || k == platform.KeyDown)) {
		e.Call("preventDefault")
	}
	s.send(platform.SurfaceEvent{Kind: kind, Key: k, Mods: m, Repeat: e.Get("repeat").Truthy()})
	// Text keys type the character the keyboard layout gives, unless a
	// modifier makes them a shortcut.
	if text := e.Get("key").String(); len(text) == 1 && m&(platform.ModCtrl|platform.ModAlt|platform.ModSuper) == 0 {
		s.send(platform.SurfaceEvent{Kind: platform.TextInput, Text: text, Mods: m})
	}
}

// keys maps the names of the keys of the page to Key.
var keys = map[string]platform.Key{
	"Enter": platform.KeyEnter, "Escape": platform.KeyEscape, "Backspace": platform.KeyBackspace,
	"Tab": platform.KeyTab, " ": platform.KeySpace, "Delete": platform.KeyDelete, "Insert": platform.KeyInsert,
	"Home": platform.KeyHome, "End": platform.KeyEnd, "PageUp": platform.KeyPageUp, "PageDown": platform.KeyPageDown,
	"ArrowLeft": platform.KeyLeft, "ArrowRight": platform.KeyRight, "ArrowUp": platform.KeyUp, "ArrowDown": platform.KeyDown,
	"F1": platform.KeyF1, "F2": platform.KeyF2, "F3": platform.KeyF3, "F4": platform.KeyF4,
	"F5": platform.KeyF5, "F6": platform.KeyF6, "F7": platform.KeyF7, "F8": platform.KeyF8,
	"F9": platform.KeyF9, "F10": platform.KeyF10, "F11": platform.KeyF11, "F12": platform.KeyF12,
	"ContextMenu": platform.KeyContextMenu,
	"-":           platform.KeyMinus, "=": platform.KeyEqual, ",": platform.KeyComma, ".": platform.KeyPeriod,
	"/": platform.KeySlash, ";": platform.KeySemicolon, "'": platform.KeyQuote,
	"[": platform.KeyBracketLeft, "]": platform.KeyBracketRight, "\\": platform.KeyBackslash, "`": platform.KeyBackquote,
}

// keyFor reads the key of a page event.
func keyFor(e js.Value) platform.Key {
	if k, ok := keys[e.Get("key").String()]; ok {
		return k
	}
	// Letters and digits follow the layout's unshifted character, which is
	// what the key code names.
	if c := e.Get("code").String(); len(c) == 4 && (c[0] == 'K' && c[1] == 'e' && c[2] == 'y') {
		return platform.KeyForRune(rune(c[3]))
	}
	if c := e.Get("code").String(); len(c) == 6 && c[:5] == "Digit" {
		return platform.KeyForRune(rune(c[5]))
	}
	if r := []rune(e.Get("key").String()); len(r) == 1 {
		return platform.KeyForRune(r[0])
	}
	return platform.KeyUnknown
}

// Native returns the objects a GPU renderer draws into; the browser's GPU
// renderer draws into the canvas instead, and only needs it non-zero so the
// host takes that path.
func (s *surface) Native() platform.SurfaceNative { return platform.SurfaceNative{HWND: 1} }

// Size returns the drawing area in DIPs and the device pixels per DIP.
func (s *surface) Size() (width, height, scale float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.measured {
		return s.width, s.height, s.scale
	}
	return s.width, s.height, s.scale
}

// measure takes the size and scale of the canvas. Main thread only.
//
// The size is the element's content box in CSS pixels, not its bounding
// rectangle. The rectangle includes the border, and the backing store the
// renderer sets is this size times the device pixel ratio: a size that is
// not the content box makes the two disagree, and the browser then resamples
// the canvas as it composites it, which blurs every glyph edge on screen
// (the canvas buffer itself stays sharp, so it is not visible from
// getImageData). The content box keeps the drawing one to one with the
// device pixels.
func (s *surface) measure() {
	scale := 1.0
	if v := js.Global().Get("devicePixelRatio"); v.Type() == js.TypeNumber && v.Float() > 0 {
		scale = v.Float()
	}
	width, height := 0.0, 0.0
	if !s.el.IsUndefined() {
		width, height = float64(s.el.Get("clientWidth").Int()), float64(s.el.Get("clientHeight").Int())
		// A CSS size that is not whole pixels, or a ratio that is not whole
		// either, leaves the device size between two pixels and resamples
		// anyway. Say so, rather than leaving a soft frame unexplained. The
		// tolerance absorbs the noise in devicePixelRatio itself (a 125%
		// display reports 1.2500000186264515, which is whole for our purpose).
		if width > 0 && height > 0 {
			dw, dh := math.Round(width*scale), math.Round(height*scale)
			switch {
			case dw <= 0 || dh <= 0:
			case math.Abs(width*scale-dw) < 0.01 && math.Abs(height*scale-dh) < 0.01:
				// devicePixelRatio carries float noise - a 125% display reports
				// 1.2500000186264515 - and the frame's device size is this size
				// times this scale, rounded up. The noise would then add a whole
				// pixel to the backing store, which the browser undoes by
				// resampling the canvas as it composites it: every glyph edge on
				// screen blurs, while the canvas buffer itself stays sharp, so
				// getImageData cannot see it. Snap the ratio so the device size
				// is exact and the drawing is one to one with the device pixels.
				scale = dw / width
			default:
				// The page sized the element so that its device box is not a
				// whole number of pixels; no ratio can make the drawing one to
				// one with them. Say so, rather than leave a soft frame
				// unexplained.
				log.Printf("mygo: canvas content box is %gx%g CSS px at %gx: the device size %gx%g is not whole pixels, so the browser resamples it; size the canvas in whole device pixels",
					width, height, scale, width*scale, height*scale)
			}
		}
	}
	if width <= 0 || height <= 0 {
		width, height = js.Global().Get("innerWidth").Float(), js.Global().Get("innerHeight").Float()
	}
	if width <= 0 {
		width = float64(s.w.bounds.Width)
	}
	if height <= 0 {
		height = float64(s.w.bounds.Height)
	}
	s.mu.Lock()
	s.width, s.height, s.scale, s.measured = width, height, scale, true
	s.mu.Unlock()
}

// RequestFrame has the host draw a frame at the page's next animation
// frame.
func (s *surface) RequestFrame() {
	s.mu.Lock()
	s.pending = true
	s.mu.Unlock()
	s.attach()
	s.b.scheduleFrame()
}

// wantsFrame reports whether a frame was asked for and not drawn yet.
func (s *surface) wantsFrame() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending
}

// tick draws the frame the content asked for. Main thread only.
func (s *surface) tick() {
	s.mu.Lock()
	pending := s.pending
	s.pending = false
	s.mu.Unlock()
	if !pending {
		return
	}
	s.send(platform.SurfaceEvent{Kind: platform.SurfaceFrame})
}

// send delivers an event to the content.
func (s *surface) send(ev platform.SurfaceEvent) { s.w.h.SurfaceEvent(ev) }

// clear wipes the canvas; a hidden window draws nothing.
func (s *surface) clear() {
	if s.el.IsUndefined() {
		return
	}
	if ctx := s.el.Call("getContext", "2d"); !ctx.IsUndefined() && !ctx.IsNull() {
		ctx.Call("clearRect", 0, 0, s.el.Get("width").Int(), s.el.Get("height").Int())
	}
}

// RefreshRate returns the page's, which the animation frames follow.
func (s *surface) RefreshRate() float64 { return 60 }

// PresentPixels shows a frame drawn in memory: the path the host takes when
// the browser renders none, as it does while the canvas is missing.
// Premultiplied BGRA rows become the straight RGBA of ImageData.
func (s *surface) PresentPixels(pix []byte, stride, width, height int) {
	if s.el.IsUndefined() || width <= 0 || height <= 0 {
		return
	}
	out := make([]byte, 4*width*height)
	for y := 0; y < height; y++ {
		row := pix[y*stride : y*stride+4*width]
		dst := out[y*4*width:]
		for x := 0; x < width; x++ {
			b, g, r, a := row[4*x], row[4*x+1], row[4*x+2], row[4*x+3]
			if a != 0 && a != 255 {
				r = byte(min(255, int(r)*255/int(a)))
				g = byte(min(255, int(g)*255/int(a)))
				b = byte(min(255, int(b)*255/int(a)))
			}
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = r, g, b, a
		}
	}
	if s.el.Get("width").Int() != width {
		s.el.Set("width", width)
	}
	if s.el.Get("height").Int() != height {
		s.el.Set("height", height)
	}
	ctx := s.el.Call("getContext", "2d")
	if ctx.IsUndefined() || ctx.IsNull() {
		return
	}
	u8 := js.Global().Get("Uint8Array").New(len(out))
	js.CopyBytesToJS(u8, out)
	clamped := js.Global().Get("Uint8ClampedArray").New(u8.Get("buffer"))
	img := js.Global().Get("ImageData").New(clamped, width, height)
	ctx.Call("putImageData", img, 0, 0)
}

// SetCursor sets the pointer's shape over the canvas.
func (s *surface) SetCursor(c platform.Cursor) {
	s.mu.Lock()
	s.cursor = c
	s.mu.Unlock()
	if s.el.IsUndefined() {
		return
	}
	s.el.Get("style").Set("cursor", cssCursor(c))
}

var cursors = map[platform.Cursor]string{
	platform.CursorDefault: "default", platform.CursorPointer: "pointer", platform.CursorText: "text",
	platform.CursorMove: "move", platform.CursorResizeEW: "ew-resize", platform.CursorResizeNS: "ns-resize",
	platform.CursorResizeNWSE: "nwse-resize", platform.CursorResizeNESW: "nesw-resize",
	platform.CursorNotAllowed: "not-allowed", platform.CursorCrosshair: "crosshair",
	platform.CursorGrab: "grab", platform.CursorGrabbing: "grabbing",
	platform.CursorResizeN: "n-resize", platform.CursorResizeE: "e-resize",
	platform.CursorResizeS: "s-resize", platform.CursorResizeW: "w-resize",
	platform.CursorResizeColumn: "col-resize", platform.CursorResizeRow: "row-resize",
	platform.CursorVerticalText: "vertical-text",
}

func cssCursor(c platform.Cursor) string {
	if v, ok := cursors[c]; ok {
		return v
	}
	return "default"
}

// SetTextInput is in text_input.go: the hidden element an input method
// composes into, which is what a page whose interface is a canvas needs.
func (s *surface) UpdateAccessibility(tree *platform.AccessTree) {}

func (s *surface) StartDataDrag(platform.DragRequest) {}

func (s *surface) CancelDataDrag() {}

func (s *surface) SetDropFormats([]transfer.Format) {}

func (s *surface) NewWebView(*platform.WindowOptions, platform.WindowHandler) (platform.WebView, error) {
	return nil, errNoPage
}

func (s *surface) PlaceWebViews([]platform.WebViewPlacement) {}

// nowMillis is performance.now(), in milliseconds.
func nowMillis() float64 {
	p := js.Global().Get("performance")
	if p.IsUndefined() || p.IsNull() {
		return float64(js.Global().Get("Date").Call("now").Float())
	}
	return p.Call("now").Float()
}
