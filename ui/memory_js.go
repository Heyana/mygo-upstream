//go:build js && wasm

package ui

import "syscall/js"

// LinearMemoryBytes returns the size of the wasm linear memory the Go
// runtime is running in, or 0 when the page did not keep a reference to it.
//
// It is the pool the Go heap is carved from, and on wasm it is the number
// that says what the app costs: a HeapAlloc of 1 MB inside a 64 MB pool
// occupies 64 MB, because the pool only grows and the browser never takes
// a page back. A page that wants this shown must keep the memory object:
//
//	window.__wasmMem = result.instance.exports.mem
//
// which the lab's index.html does; a page that does not gets 0, and a
// display should say "—" rather than invent a number.
func LinearMemoryBytes() int64 {
	mem := js.Global().Get("__wasmMem")
	if !mem.Truthy() {
		return 0
	}
	buf := mem.Get("buffer")
	if !buf.Truthy() {
		return 0
	}
	return int64(buf.Get("byteLength").Int())
}

// PageMemory is what measureUserAgentSpecificMemory reported for the page:
// the whole thing, by kind, as the browser counts it.
//
// Why a second memory reading exists next to HeapReading and
// LinearMemoryBytes, and why the three do not agree: they are three
// different questions, and a display that shows one number answers none of
// them.
//
//   - HeapReading (MemStats)  what the Go runtime's own allocator holds.
//   - LinearMemoryBytes       the wasm pool the Go heap is carved from:
//     reserved, only grows, and the whole pool is
//     the page's cost even where the heap is small.
//   - PageMemory              what the browser attributes to this page:
//     the JS heap, ArrayBuffers, WebAssembly
//     memory, and compiled code. This is the number
//     a heap snapshot is in the same units as.
//
// The reason the readings look like they "do not add up" against a heap
// snapshot is that a snapshot reports the V8 heap, and WebAssembly memory,
// ArrayBuffers and canvas bitmaps live OUTSIDE it — the heap holds a
// ~100 byte wrapper for each. A page whose heap reads 30 MB can hold 300 MB
// of backing stores.
//
// It is a Promise because Chrome waits for its next regular garbage
// collection before measuring, which can take up to about twenty seconds:
// do not await it on a path that has to be prompt, and do not compare two
// readings taken a second apart. It is a sampling API for trends.
//
// The byte values are not comparable across browsers or versions, and how
// the breakdown is attributed is up to the browser: nothing here assumes a
// shape, it sums whatever entries arrive.
type PageMemory struct {
	// Total is every byte the browser attributes to the page, and 0 when
	// the API is unavailable (no cross-origin isolation, or not Chromium)
	// or the measurement failed. A display should say "unavailable" rather
	// than draw a zero.
	Total int64
	// JS and DOM are the totals per reported kind, summed across
	// attribution entries. Kinds the browser leaves unnamed go to Other.
	JS, DOM, Other int64
	// Err is why there is no reading, for a display that wants to say.
	Err string
}

// PageMemoryNow asks the browser for a page measurement. The returned
// channel delivers exactly one PageMemory and is then closed: a Promise
// cannot be read synchronously, and blocking the UI thread on the next GC
// would be worse than the reading is worth.
//
// Callers read it without blocking:
//
//	select {
//	case m := <-ui.PageMemoryNow():
//		last = m
//	default: // keep showing the previous reading
//	}
func PageMemoryNow() <-chan PageMemory {
	out := make(chan PageMemory, 1)
	measure := js.Global().Get("performance").Get("measureUserAgentSpecificMemory")
	if !measure.Truthy() {
		out <- PageMemory{Err: "unavailable (needs cross-origin isolation)"}
		close(out)
		return out
	}
	var resolve, reject js.Func
	resolve = js.FuncOf(func(_ js.Value, args []js.Value) any {
		var m PageMemory
		r := args[0]
		m.Total = int64(r.Get("bytes").Int())
		if bd := r.Get("breakdown"); bd.Truthy() {
			n := bd.Length()
			for i := 0; i < n; i++ {
				e := bd.Index(i)
				b := int64(e.Get("bytes").Int())
				// types is "an array of implementation-defined memory
				// types": sum by what it says, do not assume it says
				// anything in particular.
				switch types := e.Get("types"); {
				case !types.Truthy():
					m.Other += b
				default:
					named := false
					for j := 0; j < types.Length(); j++ {
						switch types.Index(j).String() {
						case "JavaScript":
							m.JS += b
							named = true
						case "DOM":
							m.DOM += b
							named = true
						}
					}
					if !named {
						m.Other += b
					}
				}
			}
		}
		resolve.Release()
		reject.Release()
		out <- m
		close(out)
		return nil
	})
	reject = js.FuncOf(func(_ js.Value, args []js.Value) any {
		resolve.Release()
		reject.Release()
		out <- PageMemory{Err: "measurement failed"}
		close(out)
		return nil
	})
	// Not a Promise rejection path we can do much about: the only errors
	// are no isolation and no API, and the first is checked above by the
	// method being absent.
	p := measure.Invoke()
	p.Call("then", resolve).Call("catch", reject)
	return out
}
