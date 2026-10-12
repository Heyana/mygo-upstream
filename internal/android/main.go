//go:build android && cgo

// Command android is the Go half of mygo's Android host: the main package
// built into libmygo.so, which the Activity loads with
// System.loadLibrary("mygo").
//
// It is a main package because -buildmode=c-shared requires exactly one, and
// everything it exposes to Java is the //export functions in its imports.
//
// Those exports used to live here, and they cannot: a main package cannot be
// imported, so an app (a separate module) could not reach them — and an app
// has to reach them, because c-shared does not call main (below). They live in
// android/jni now, which any main package can import. Importing it for its
// exports is why the blank import below carries a comment; that is the whole
// mechanism.
package main

import (
	_ "github.com/egoist/mygo/android/jni"
)

// main exists only because the buildmode requires a main package: in
// c-shared mode the Go runtime starts when the library is loaded but does
// not call main, and the Java host drives everything through the //export
// functions, so there is nothing to do here. It is empty rather than
// blocking, as the cgo c-shared examples are, so that it is harmless
// whichever way a future runtime treats it.
//
// An app cannot start itself from here. Package init, which does run when the
// runtime starts, is the only hook (see an app's own android-tagged file).
func main() {}
