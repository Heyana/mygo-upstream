//go:build js

package web

// Package web is the browser backend; this file carries the one method this
// branch added to platform.Window for phones, which no browser can honour.
// It stays here so the tree keeps upstream's file and still compiles.

import "github.com/egoist/mygo/internal/platform"

func (w *window) SafeArea() platform.SafeArea { return platform.SafeArea{} }
