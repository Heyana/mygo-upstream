//go:build !(js && wasm)

package ui

// LinearMemoryBytes returns 0 on the platforms that have no wasm linear
// memory. Desktop runtimes take memory from the OS a page at a time and
// can map pages back, so there is no pool to report: MemStats.Sys (see
// HeapReading) is the number for those, and it is why a display shows
// both and labels them.
func LinearMemoryBytes() int64 { return 0 }

// PageMemory is the wasm reading of what the browser attributes to the
// page; on a desktop there is no browser to ask, and a window's memory is
// the OS's number to give, not the app's. See memory_js.go for what the
// reading means where it exists.
type PageMemory struct {
	Total          int64
	JS, DOM, Other int64
	Err            string
}

// PageMemoryNow reports that there is nothing to report on this platform.
// It is a channel to keep the signature one thing across platforms: a
// caller writes its select once, and a desktop build takes the error
// branch without a build tag of its own.
func PageMemoryNow() <-chan PageMemory {
	out := make(chan PageMemory, 1)
	out <- PageMemory{Err: "unavailable (no browser)"}
	close(out)
	return out
}
