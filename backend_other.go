//go:build (android && !cgo) || (ios && !cgo) || (!darwin && !(linux && (amd64 || arm64)) && !(windows && (amd64 || arm64)) && !(js && wasm))

package mygo

import (
	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/unsupported"
)

func newBackend() platform.Backend { return unsupported.New() }
