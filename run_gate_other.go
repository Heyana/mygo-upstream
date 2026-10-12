//go:build !android

package mygo

// runFromAnyGoroutine reports that App.Run may be called from a goroutine
// other than the main one. It cannot on the desktop platforms: the event loop
// must own the thread the process started with, so Run has to be main's last
// statement (see Application.Run).
func runFromAnyGoroutine() bool { return false }
