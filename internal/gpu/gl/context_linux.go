//go:build linux && (amd64 || arm64) && !android

package gl

// currentContext names the GL context the renderer's textures belong to, so
// that a renderer whose context changed can drop them rather than use
// another context's names (gl.go:533 and :794, present.go:30).
//
// On Linux the answer comes from GDK, when the app has it loaded: GTK makes
// its area's context current before it draws, and GTK realizes the area
// again after it was hidden. A window that is not GTK's (a test, a
// headless run) gets 0, which is what a caller here tests against.

func currentContext() uintptr {
	if currentGDKContext == nil {
		return 0
	}
	return currentGDKContext()
}
