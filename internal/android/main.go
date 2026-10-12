//go:build android && cgo

// Command android is the Go half of mygo's Android host: the main package
// built into libmygo.so, which the Activity loads with
// System.loadLibrary("mygo").
//
// It is a main package because -buildmode=c-shared requires exactly one,
// and everything it exposes to Java is the //export functions in jni.go.
// The backend itself lives in internal/android/backend, which this package
// and mygo's backend_android.go both use, since a main package cannot be
// imported.
package main

// main exists only because the buildmode requires a main package: in
// c-shared mode the Go runtime starts when the library is loaded but does
// not call main, and the Java host drives everything through the //export
// functions, so there is nothing to do here. It is empty rather than
// blocking, as the cgo c-shared examples are, so that it is harmless
// whichever way a future runtime treats it.
func main() {}
