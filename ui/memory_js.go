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
