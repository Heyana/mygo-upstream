//go:build !(js && wasm)

package ui

// LinearMemoryBytes returns 0 on the platforms that have no wasm linear
// memory. Desktop runtimes take memory from the OS a page at a time and
// can map pages back, so there is no pool to report: MemStats.Sys (see
// HeapReading) is the number for those, and it is why a display shows
// both and labels them.
func LinearMemoryBytes() int64 { return 0 }
