//go:build android && cgo

package mygo

import (
	// Aliased: package mygo already has an identifier named backend
	// (mygo.go's backend(), which returns the platform.Backend).
	androidbackend "github.com/egoist/mygo/internal/android/backend"

	"github.com/egoist/mygo/internal/platform"
)

// newBackend returns the Android backend.
//
// The library the Java host loads (System.loadLibrary("mygo")) is built
// from internal/android, whose package main holds the JNI //export
// functions; -buildmode=c-shared requires a main package and a main package
// cannot be imported, so the backend itself is internal/android/backend and
// both the library and package mygo use it.
//
// The cgo condition matters: internal/android/backend needs cgo (EGL, JNI),
// so this file must not be selected when cgo is off, or GOOS=android with
// CGO_ENABLED=0 would fail on a package with no buildable files. With cgo
// off, GOOS=android keeps falling back to internal/unsupported through
// backend_other.go.
func newBackend() platform.Backend { return androidbackend.New() }
