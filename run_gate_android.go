//go:build android

package mygo

// runFromAnyGoroutine reports that App.Run may be called from a goroutine
// other than the main one — true on Android, where there is no main to be the
// main goroutine in the first place.
//
// Java owns the process and loads the library, so the Go runtime has no
// goroutine that owns the "UI thread"; the backend's pump is what every UI
// call must run on (see internal/android/backend.IsMainThread), and an app's
// main runs on a goroutine of its own because a JNI entry point has to return
// (see android/jni.Start). Requiring main to be the caller would mean an app
// could never start.
//
// Run's work still lands on the pump: the backend's Run waits for it, and the
// app's main goroutine is a "other goroutine" for everything package mygo
// hands to the main thread (postMain, WhenReady, await), which is the path
// that exists for exactly this case.
func runFromAnyGoroutine() bool { return true }
