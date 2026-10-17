//go:build ios || (!windows && !darwin && !android && !(linux && (amd64 || arm64)) && !(js && wasm))

package ui

import (
	"github.com/egoist/mygo/internal/platform"
)

func newGPURenderer(platform.SurfaceNative) (gpuRenderer, error) {
	return nil, errors.New("no GPU renderer for this platform yet")
}
